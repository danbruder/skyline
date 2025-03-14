package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/danbruder/skyline/internal/config"
	"github.com/danbruder/skyline/pkg/events"
)

// ProcessInfo stores information about a running process
type ProcessInfo struct {
	AppID     string
	Cmd       *exec.Cmd
	StartTime time.Time
	Restarts  int
	Status    string // running, stopped, crashed
}

// Supervisor manages application processes
type Supervisor struct {
	ctx      context.Context
	cfg      config.SupervisorConfig
	logger   *log.Logger
	eventBus *events.EventBus
	procs    map[string]*ProcessInfo
	mu       sync.RWMutex
}

// New creates a new supervisor
func New(ctx context.Context, cfg config.SupervisorConfig, logger *log.Logger, eventBus *events.EventBus) *Supervisor {
	return &Supervisor{
		ctx:      ctx,
		cfg:      cfg,
		logger:   logger,
		eventBus: eventBus,
		procs:    make(map[string]*ProcessInfo),
	}
}

// Start starts the supervisor
func (s *Supervisor) Start() error {
	s.logger.Println("Starting supervisor...")

	// Create apps directory if it doesn't exist
	if err := os.MkdirAll(s.cfg.AppsDir, 0755); err != nil {
		return fmt.Errorf("failed to create apps directory: %w", err)
	}

	// Start monitoring loop
	go s.monitorProcesses()

	return nil
}

// Stop stops the supervisor and all managed processes
func (s *Supervisor) Stop() {
	s.logger.Println("Stopping supervisor...")

	s.mu.Lock()
	defer s.mu.Unlock()

	for appID, proc := range s.procs {
		s.logger.Printf("Stopping app %s...", appID)
		if err := s.stopProcess(proc); err != nil {
			s.logger.Printf("Error stopping app %s: %v", appID, err)
		}
	}
}

// StartApp starts an application
func (s *Supervisor) StartApp(appID, execPath string, env []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if app is already running
	if proc, exists := s.procs[appID]; exists && proc.Status == "running" {
		return fmt.Errorf("app %s is already running", appID)
	}

	// Start the process
	cmd := exec.CommandContext(s.ctx, execPath)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = filepath.Join(s.cfg.AppsDir, appID)

	// Setup stdout and stderr
	logFile, err := os.OpenFile(
		filepath.Join(cmd.Dir, "app.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start the process
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start app: %w", err)
	}

	// Store process info
	proc := &ProcessInfo{
		AppID:     appID,
		Cmd:       cmd,
		StartTime: time.Now(),
		Status:    "running",
		Restarts:  0,
	}
	s.procs[appID] = proc

	// Publish event
	s.eventBus.Publish(events.Event{
		Type:    events.AppStarted,
		AppID:   appID,
		Message: fmt.Sprintf("App %s started with PID %d", appID, cmd.Process.Pid),
	})

	// Monitor process in background
	go s.waitForProcess(proc, logFile)

	return nil
}

// StopApp stops an application
func (s *Supervisor) StopApp(appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	proc, exists := s.procs[appID]
	if !exists {
		return fmt.Errorf("app %s is not managed by supervisor", appID)
	}

	return s.stopProcess(proc)
}

// RestartApp restarts an application
func (s *Supervisor) RestartApp(appID string) error {
	s.mu.RLock()
	proc, exists := s.procs[appID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("app %s is not managed by supervisor", appID)
	}

	// Get app details
	execPath := proc.Cmd.Path
	env := proc.Cmd.Env

	// Stop the app
	if err := s.StopApp(appID); err != nil {
		return fmt.Errorf("failed to stop app: %w", err)
	}

	// Start the app again
	time.Sleep(500 * time.Millisecond) // Small delay to ensure cleanup
	return s.StartApp(appID, execPath, env)
}

// GetStatus returns the status of an application
func (s *Supervisor) GetStatus(appID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	proc, exists := s.procs[appID]
	if !exists {
		return "", fmt.Errorf("app %s is not managed by supervisor", appID)
	}

	return proc.Status, nil
}

// ListApps returns a list of managed applications
func (s *Supervisor) ListApps() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	apps := make([]string, 0, len(s.procs))
	for appID := range s.procs {
		apps = append(apps, appID)
	}

	return apps
}

// Private methods

func (s *Supervisor) stopProcess(proc *ProcessInfo) error {
	if proc.Status != "running" {
		return nil
	}

	// Send SIGTERM
	if err := proc.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.logger.Printf("Failed to send SIGTERM to app %s: %v", proc.AppID, err)

		// Fallback to SIGKILL
		if err := proc.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	// Wait for process to exit (with timeout)
	done := make(chan error, 1)
	go func() {
		_, err := proc.Cmd.Process.Wait()
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("error waiting for process to exit: %w", err)
		}
	case <-time.After(5 * time.Second):
		// Force kill after timeout
		if err := proc.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to force kill process: %w", err)
		}
	}

	proc.Status = "stopped"

	// Publish event
	s.eventBus.Publish(events.Event{
		Type:    events.AppStopped,
		AppID:   proc.AppID,
		Message: fmt.Sprintf("App %s stopped", proc.AppID),
	})

	return nil
}

func (s *Supervisor) waitForProcess(proc *ProcessInfo, logFile *os.File) {
	defer logFile.Close()

	// Wait for process to exit
	err := proc.Cmd.Wait()

	s.mu.Lock()
	if s.ctx.Err() != nil || proc.Status == "stopped" {
		// Context cancelled or process stopped intentionally
		s.mu.Unlock()
		return
	}

	// Process exited unexpectedly
	proc.Status = "crashed"
	s.mu.Unlock()

	// Publish event
	errMsg := "Process exited normally"
	if err != nil {
		errMsg = fmt.Sprintf("Process exited with error: %v", err)
	}

	s.eventBus.Publish(events.Event{
		Type:    events.AppFailed,
		AppID:   proc.AppID,
		Message: errMsg,
	})

	// Attempt to restart if configured to do so
	s.mu.Lock()
	restarts := proc.Restarts
	s.mu.Unlock()

	if restarts < s.cfg.MaxRestarts {
		s.logger.Printf("Restarting app %s (attempt %d/%d)...",
			proc.AppID, restarts+1, s.cfg.MaxRestarts)

		// Wait before restarting
		time.Sleep(s.cfg.RestartDelay)

		// Restart the app
		s.mu.Lock()
		proc.Restarts++
		s.mu.Unlock()

		if err := s.RestartApp(proc.AppID); err != nil {
			s.logger.Printf("Failed to restart app %s: %v", proc.AppID, err)
		}
	} else {
		s.logger.Printf("App %s exceeded maximum restart attempts (%d)",
			proc.AppID, s.cfg.MaxRestarts)
	}
}

func (s *Supervisor) monitorProcesses() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkProcesses()
		}
	}
}

func (s *Supervisor) checkProcesses() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for appID, proc := range s.procs {
		if proc.Status == "running" {
			// Check if process is still running
			if err := proc.Cmd.Process.Signal(syscall.Signal(0)); err != nil {
				s.logger.Printf("App %s seems to have died outside our control: %v", appID, err)

				// Trigger a restart
				go func(id string) {
					if err := s.RestartApp(id); err != nil {
						s.logger.Printf("Failed to restart app %s: %v", id, err)
					}
				}(appID)
			}
		}
	}
}
