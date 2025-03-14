package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/pkg/events"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// AppCreateRequest is the request body for creating an app
type AppCreateRequest struct {
	Name        string            `json:"name"`
	RepoURL     string            `json:"repo_url"`
	Branch      string            `json:"branch"`
	Domain      string            `json:"domain"`
	Environment map[string]string `json:"environment,omitempty"`
}

// DeployRequest is the request body for deploying an app
type DeployRequest struct {
	CommitSHA string `json:"commit_sha,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

// GitHub webhook event types
const (
	GithubEventPush = "push"
	GithubEventPing = "ping"
)

// Complete the handler implementations in server.go

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// Parse deploy request
	var req DeployRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.respondError(w, r, err, http.StatusBadRequest)
		return
	}

	// Create deployment record
	deployID := uuid.New().String()
	deployment := &db.Deployment{
		ID:        deployID,
		AppID:     appID,
		CommitSHA: req.CommitSHA,
		Status:    "pending",
		StartedAt: time.Now(),
	}

	if err := s.db.CreateDeployment(r.Context(), deployment); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	// Start deployment in background
	go s.performDeploy(r, app, deployment)

	s.respond(w, r, deployment, http.StatusAccepted)
}

func (s *Server) handleStartApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// TODO: Implement app start with the supervisor
	// For MVP, we'll just update the status
	app.Status = "running"
	app.UpdatedAt = time.Now()

	if err := s.db.UpdateApp(r.Context(), app); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, app, http.StatusOK)
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// TODO: Implement app stop with the supervisor
	// For MVP, we'll just update the status
	app.Status = "stopped"
	app.UpdatedAt = time.Now()

	if err := s.db.UpdateApp(r.Context(), app); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, app, http.StatusOK)
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// TODO: Implement app restart with the supervisor
	// For MVP, we'll just update the status
	app.Status = "running"
	app.UpdatedAt = time.Now()

	if err := s.db.UpdateApp(r.Context(), app); err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, app, http.StatusOK)
}

func (s *Server) handleGetAppStatus(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	app, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// Return status
	s.respond(w, r, map[string]string{"status": app.Status}, http.StatusOK)
}

func (s *Server) handleGetAppLogs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	// Get app
	_, err := s.db.GetApp(r.Context(), appID)
	if err != nil {
		s.respondError(w, r, err, http.StatusNotFound)
		return
	}

	// Parse query parameters
	lines := 100
	if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
		if parsed, err := strconv.Atoi(linesParam); err == nil && parsed > 0 {
			lines = parsed
		}
	}

	// Read logs file
	logsPath := filepath.Join("data", "apps", appID, "app.log")
	logs, err := s.readLastLines(logsPath, lines)
	if err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}

	s.respond(w, r, map[string]string{"logs": logs}, http.StatusOK)
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	// Verify webhook signature if secret is set
	// TODO: Implement proper verification

	// Get event type
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		s.respondError(w, r, fmt.Errorf("missing X-GitHub-Event header"), http.StatusBadRequest)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.respondError(w, r, err, http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// Handle different events
	switch eventType {
	case GithubEventPing:
		s.respond(w, r, map[string]string{"status": "pong"}, http.StatusOK)
		return
	case GithubEventPush:
		var pushEvent struct {
			Ref        string `json:"ref"`
			Repository struct {
				HTMLURL string `json:"html_url"`
			} `json:"repository"`
			HeadCommit struct {
				ID string `json:"id"`
			} `json:"head_commit"`
		}

		if err := json.Unmarshal(body, &pushEvent); err != nil {
			s.respondError(w, r, err, http.StatusBadRequest)
			return
		}

		// Extract branch from ref (refs/heads/master -> master)
		branch := pushEvent.Ref
		if len(branch) > 11 && branch[:11] == "refs/heads/" {
			branch = branch[11:]
		}

		// Find apps that use this repository and branch
		apps, err := s.db.ListApps(r.Context())
		if err != nil {
			s.respondError(w, r, err, http.StatusInternalServerError)
			return
		}

		for _, app := range apps {
			if app.RepoURL == pushEvent.Repository.HTMLURL && app.Branch == branch {
				// Create deployment
				deployID := uuid.New().String()
				deployment := &db.Deployment{
					ID:        deployID,
					AppID:     app.ID,
					CommitSHA: pushEvent.HeadCommit.ID,
					Status:    "pending",
					StartedAt: time.Now(),
				}

				if err := s.db.CreateDeployment(r.Context(), deployment); err != nil {
					s.logger.Printf("Error creating deployment: %v", err)
					continue
				}

				// Start deployment in background
				go s.performDeploy(r, app, deployment)
			}
		}

		s.respond(w, r, map[string]string{"status": "processing"}, http.StatusOK)
		return
	default:
		s.respond(w, r, map[string]string{"status": "ignored", "event": eventType}, http.StatusOK)
	}
}

// Helper methods

func (s *Server) performDeploy(r *http.Request, app *db.App, deployment *db.Deployment) {
	s.logger.Printf("Starting deployment %s for app %s", deployment.ID, app.ID)

	// Update deployment status
	deployment.Status = "in_progress"
	s.db.UpdateDeployment(r.Context(), deployment)

	// TODO: Implement actual deployment logic
	// For MVP, we'll just simulate a successful deployment
	time.Sleep(2 * time.Second)

	// Update deployment status
	deployment.Status = "success"
	deployment.EndedAt = time.Now()
	s.db.UpdateDeployment(r.Context(), deployment)

	// Update app status
	app.Status = "running"
	app.LastDeploy = time.Now()
	app.UpdatedAt = time.Now()
	s.db.UpdateApp(r.Context(), app)

	// Publish event
	s.eventBus.Publish(events.Event{
		Type:    events.AppDeployed,
		AppID:   app.ID,
		Message: fmt.Sprintf("App %s deployed successfully", app.Name),
	})
}

func (s *Server) readLastLines(filePath string, lineCount int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", err
	}

	fileSize := stat.Size()
	if fileSize == 0 {
		return "", nil
	}

	// Start with a reasonable buffer size
	bufferSize := int64(1024)
	if bufferSize > fileSize {
		bufferSize = fileSize
	}

	// Read from the end of the file
	lines := make([]string, 0, lineCount)
	buffer := make([]byte, bufferSize)
	position := fileSize

	for len(lines) < lineCount && position > 0 {
		readSize := bufferSize
		if position < bufferSize {
			readSize = position
		}
		position -= readSize

		_, err := file.Seek(position, io.SeekStart)
		if err != nil {
			return "", err
		}

		bytesRead, err := file.Read(buffer[:readSize])
		if err != nil {
			return "", err
		}

		// Process buffer
		for i := bytesRead - 1; i >= 0; i-- {
			if buffer[i] == '\n' || i == 0 {
				// Found a newline
				if len(lines) < lineCount {
					start := i
					if buffer[i] == '\n' {
						start = i + 1
					}
					if start < bytesRead {
						lines = append(lines, string(buffer[start:bytesRead]))
					}
				}
				bytesRead = i
			}
		}
	}

	// Reverse the lines to get them in the correct order
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	byteLines := make([][]byte, len(lines))
	for i, line := range lines {
		byteLines[i] = []byte(line)
	}
	return string(bytes.Join(byteLines, []byte("\n"))), nil
}
