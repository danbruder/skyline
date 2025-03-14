package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/danbruder/skyline/internal/config"
	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/pkg/events"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server is the API server
type Server struct {
	cfg      config.APIConfig
	logger   *log.Logger
	router   *chi.Mux
	db       *db.Database
	eventBus *events.EventBus
	server   *http.Server
}

// NewServer creates a new API server
func NewServer(cfg config.APIConfig, logger *log.Logger, database *db.Database, eventBus *events.EventBus) *Server {
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 30 * time.Second
	}

	s := &Server{
		cfg:      cfg,
		logger:   logger,
		db:       database,
		eventBus: eventBus,
		router:   chi.NewRouter(),
	}

	// Set up middleware
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.Timeout(60 * time.Second))

	// API routes
	s.router.Route("/api", func(r chi.Router) {
		r.Route("/v1", func(r chi.Router) {
			// Apps
			r.Route("/apps", func(r chi.Router) {
				r.Get("/", s.handleListApps)
				r.Post("/", s.handleCreateApp)
				r.Route("/{appID}", func(r chi.Router) {
					r.Get("/", s.handleGetApp)
					r.Put("/", s.handleUpdateApp)
					r.Delete("/", s.handleDeleteApp)
					r.Post("/deploy", s.handleDeployApp)
					r.Post("/start", s.handleStartApp)
					r.Post("/stop", s.handleStopApp)
					r.Post("/restart", s.handleRestartApp)
					r.Get("/status", s.handleGetAppStatus)
					r.Get("/logs", s.handleGetAppLogs)
					r.Get("/deployments", s.handleListDeployments)
					r.Get("/backups", s.handleListBackups)
				})
			})

			// GitHub webhooks
			r.Post("/webhooks/github", s.handleGitHubWebhook)
		})
	})

	// Static UI files
	if cfg.UIDir != "" {
		s.router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			fs := http.FileServer(http.Dir(cfg.UIDir))
			if r.URL.Path != "/" && filepath.Ext(r.URL.Path) == "" {
				http.ServeFile(w, r, filepath.Join(cfg.UIDir, "index.html"))
				return
			}
			fs.ServeHTTP(w, r)
		})
	}

	return s
}

// Start starts the API server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	s.logger.Printf("Starting API server on %s", addr)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
	}

	return s.server.ListenAndServe()
}

// Stop stops the API server
func (s *Server) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		s.logger.Println("Stopping API server...")
		if err := s.server.Shutdown(ctx); err != nil {
			s.logger.Printf("Error shutting down API server: %v", err)
		}
	}
}

// Helper functions

func (s *Server) respond(w http.ResponseWriter, r *http.Request, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			s.logger.Printf("Error encoding response: %v", err)
		}
	}
}

func (s *Server) respondError(w http.ResponseWriter, r *http.Request, err error, status int) {
	s.respond(w, r, map[string]string{"error": err.Error()}, status)
}

func (s *Server) decodeJSON(r *http.Request, v interface{}) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

// Handler methods for apps

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.db.ListApps(r.Context())
	if err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, apps, http.StatusOK)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	s.respond(w, r, app, http.StatusOK)
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var app db.App
	if err := s.decodeJSON(r, &app); err != nil {
		s.respondError(w, r, err, http.StatusBadRequest)
		return
	}

	// Validate app
	if app.Name == "" || app.RepoURL == "" || app.Branch == "" || app.Domain == "" {
		s.respondError(w, r, fmt.Errorf("missing required fields"), http.StatusBadRequest)
		return
	}

	// Set defaults
	app.Status = "pending"
	app.CreatedAt = time.Now()
	app.UpdatedAt = time.Now()

	if err := s.db.CreateApp(r.Context(), &app); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	// Publish event
	s.eventBus.Publish(events.Event{
		Type:    events.AppDeployed,
		AppID:   app.ID,
		Message: fmt.Sprintf("App %s created", app.Name),
	})

	s.respond(w, r, app, http.StatusCreated)
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get existing app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// Decode updates
	var updates db.App
	if err := s.decodeJSON(r, &updates); err != nil {
		s.respondError(w, r, err, http.StatusBadRequest)
		return
	}

	// Apply updates
	if updates.Name != "" {
		app.Name = updates.Name
	}
	if updates.Branch != "" {
		app.Branch = updates.Branch
	}
	if updates.Domain != "" {
		app.Domain = updates.Domain
	}
	if updates.Port != 0 {
		app.Port = updates.Port
	}
	if len(updates.Environment) > 0 {
		app.Environment = updates.Environment
	}

	app.UpdatedAt = time.Now()

	if err := s.db.UpdateApp(r.Context(), app); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, app, http.StatusOK)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	if err := s.db.DeleteApp(r.Context(), appID); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, nil, http.StatusNoContent)
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	deployments, err := s.db.ListDeployments(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, deployments, http.StatusOK)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	backups, err := s.db.ListBackups(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, backups, http.StatusOK)
}
