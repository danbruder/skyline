package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/danbruder/skyline/internal/api"
	"github.com/danbruder/skyline/internal/config"
	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/internal/proxy"
	"github.com/danbruder/skyline/internal/supervisor"
	"github.com/danbruder/skyline/pkg/errors"
	"github.com/danbruder/skyline/pkg/events"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Initialize logger
	logger := log.New(os.Stdout, "[skyline] ", log.LstdFlags)
	logger.Println("Starting deployment platform...")

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize event bus for internal communication
	eventBus := events.NewEventBus()

	standardLogger := errors.NewStandardLogger(logger)

	// Initialize database
	database, err := db.New(ctx, cfg.Database.Path, standardLogger)
	if err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Run migrations
	// if err := database.Migrate(); err != nil {
	//   logger.Fatalf("Failed to run migrations: %v", err)
	// }

	// Initialize supervisor
	sup := supervisor.New(ctx, cfg.Supervisor, logger, eventBus)
	go func() {
		if err := sup.Start(); err != nil {
			logger.Printf("Supervisor error: %v", err)
			cancel()
		}
	}()

	// Initialize Caddy proxy
	proxyManager := proxy.NewCaddyManager(cfg.Proxy, logger)
	if err := proxyManager.Start(); err != nil {
		logger.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxyManager.Stop()

	// Initialize API server
	apiServer := api.NewServer(cfg.API, logger, database, eventBus)
	go func() {
		if err := apiServer.Start(); err != nil {
			logger.Printf("API server error: %v", err)
			cancel()
		}
	}()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Printf("Received signal: %v, shutting down...", sig)
	case <-ctx.Done():
		logger.Println("Context cancelled, shutting down...")
	}

	// Cleanup and graceful shutdown logic
	apiServer.Stop()
	sup.Stop()
	logger.Println("Deployment platform stopped")
}
