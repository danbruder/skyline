package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/danbruder/skyline/internal/api"
	"github.com/danbruder/skyline/internal/config"
	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/internal/proxy"
	"github.com/danbruder/skyline/internal/supervisor"
	"github.com/danbruder/skyline/pkg/errors"
	"github.com/danbruder/skyline/pkg/events"
)

//go:embed scripts/setup.sh
var embedFS embed.FS

const (
	defaultConfigPath = "config.yaml"
)

func main() {
	// Define command-line flags
	rootCmd := flag.NewFlagSet("skyline", flag.ExitOnError)
	configPath := rootCmd.String("config", defaultConfigPath, "Path to configuration file")

	// Define subcommands
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	serveConfigPath := serveCmd.String("config", defaultConfigPath, "Path to configuration file")

	// Check if any command-line arguments are provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Parse the subcommand
	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		runServer(*serveConfigPath)
	case "setup":
		runSetup(false)
	case "version":
		fmt.Println("Skyline Deployment Platform v0.1.0")
	case "help":
		printUsage()
	default:
		// Handle the case when the first argument is a flag, not a subcommand
		rootCmd.Parse(os.Args[1:])
		if rootCmd.Parsed() {
			runServer(*configPath)
		} else {
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Println("Skyline Deployment Platform")
	fmt.Println("\nUsage:")
	fmt.Println("  skyline [command] [options]")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  serve     Start the Skyline server")
	fmt.Println("  setup     Prepare the host for running Skyline")
	fmt.Println("  version   Print the version information")
	fmt.Println("  help      Show this help message")
	fmt.Println("\nOptions:")
	fmt.Println("  -config   Path to configuration file (default: config.yaml)")
	fmt.Println("\nUse \"skyline [command] --help\" for more information about a command.")
}

func runSetup(force bool) {
	logger := log.New(os.Stdout, "[skyline] ", log.LstdFlags)
	logger.Println("Running setup...")

	// Check if running as root
	if os.Geteuid() != 0 {
		logger.Println("Setup requires root privileges. Please run with sudo.")
		os.Exit(1)
	}

	// Extract the embedded script
	scriptContent, err := embedFS.ReadFile("scripts/install.sh")
	if err != nil {
		logger.Fatalf("Failed to read embedded script: %v", err)
	}

	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "skyline-setup")
	if err != nil {
		logger.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create the script file
	scriptPath := filepath.Join(tempDir, "install.sh")
	if err := os.WriteFile(scriptPath, scriptContent, 0755); err != nil {
		logger.Fatalf("Failed to write setup script: %v", err)
	}

	// Build the command
	cmd := exec.Command("bash", scriptPath)
	if force {
		cmd.Env = append(os.Environ(), "FORCE=true")
	}

	// Connect to standard output and error
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the script
	logger.Println("Executing setup script...")
	if err := cmd.Run(); err != nil {
		logger.Fatalf("Setup script failed: %v", err)
	}

	logger.Println("Setup completed successfully")
}
func runServer(configPath string) {
	// Initialize logger
	logger := log.New(os.Stdout, "[skyline] ", log.LstdFlags)
	logger.Println("Starting Skyline deployment platform...")

	// Load configuration
	cfg, err := config.Load(configPath)
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
