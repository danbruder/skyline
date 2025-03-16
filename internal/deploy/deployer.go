package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/pkg/errors"
)

// AppDeployer defines the interface for deploying applications
type AppDeployer interface {
	Deploy(ctx context.Context, buildResult BuildResult, appID string) error
	Undeploy(ctx context.Context, appID string) error
}

// SupervisorClient defines the interface for interacting with the supervisor
type SupervisorClient interface {
	StartApp(appID, execPath string, env []string) error
	StopApp(appID string) error
	RestartApp(appID string) error
	GetStatus(appID string) (string, error)
}

// ProxyClient defines the interface for interacting with the proxy
type ProxyClient interface {
	AddRoute(appID, domain string, port int) error
	RemoveRoute(appID string) error
}

// BackupClient defines the interface for interacting with the backup system
type BackupClient interface {
	AddDatabase(appID, dbPath string) error
	RemoveDatabase(appID string) error
}

// DeployConfig contains configuration for the deployer
type DeployConfig struct {
	AppsDir         string
	DataDir         string
	DefaultPort     int
	DefaultEnv      map[string]string
	DeployTimeout   time.Duration
	BackupDatabases bool
}

// Deployer implements AppDeployer
type Deployer struct {
	config     DeployConfig
	logger     errors.Logger
	database   *db.Database
	supervisor SupervisorClient
	proxy      ProxyClient
	backup     BackupClient
}

// NewDeployer creates a new Deployer
func NewDeployer(
	config DeployConfig,
	logger errors.Logger,
	database *db.Database,
	supervisor SupervisorClient,
	proxy ProxyClient,
	backup BackupClient,
) *Deployer {
	// Set defaults
	if config.AppsDir == "" {
		config.AppsDir = "data/apps"
	}
	if config.DataDir == "" {
		config.DataDir = "data/app-data"
	}
	if config.DefaultPort == 0 {
		config.DefaultPort = 8080
	}
	if config.DeployTimeout == 0 {
		config.DeployTimeout = 5 * time.Minute
	}
	if config.DefaultEnv == nil {
		config.DefaultEnv = make(map[string]string)
	}

	return &Deployer{
		config:     config,
		logger:     logger,
		database:   database,
		supervisor: supervisor,
		proxy:      proxy,
		backup:     backup,
	}
}

// Deploy deploys an application
func (d *Deployer) Deploy(ctx context.Context, buildResult BuildResult, appID string) error {
	fields := errors.FieldMap{
		"app_id":      appID,
		"binary_path": buildResult.BinaryPath,
		"app_type":    buildResult.Type,
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, d.config.DeployTimeout)
	defer cancel()

	d.logger.Info(timeoutCtx, "Deploying application", fields)

	// Get app details from database
	app, err := d.database.GetApp(timeoutCtx, appID)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to get app details")
		d.logger.Error(timeoutCtx, wrappedErr, "App retrieval failed", fields)
		return wrappedErr
	}

	fields["app_name"] = app.Name
	fields["domain"] = app.Domain

	// Create app directory structure
	appDir := filepath.Join(d.config.AppsDir, appID)
	binDir := filepath.Join(appDir, "bin")
	dataDir := filepath.Join(d.config.DataDir, appID)
	dbDir := filepath.Join(dataDir, "db")
	logDir := filepath.Join(appDir, "logs")

	// Ensure all directories exist
	for _, dir := range []string{appDir, binDir, dataDir, dbDir, logDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			wrappedErr := errors.Wrap(err, "failed to create directory")
			d.logger.Error(timeoutCtx, wrappedErr, "Directory creation failed",
				errors.WithField(fields, "directory", dir))
			return wrappedErr
		}
	}

	// Copy binary to app directory
	appBinaryPath := filepath.Join(binDir, "app")
	if err := copyFile(buildResult.BinaryPath, appBinaryPath); err != nil {
		wrappedErr := errors.Wrap(err, "failed to copy binary")
		d.logger.Error(timeoutCtx, wrappedErr, "Binary copy failed", fields)
		return wrappedErr
	}

	// Make binary executable
	if err := os.Chmod(appBinaryPath, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to make binary executable")
		d.logger.Error(timeoutCtx, wrappedErr, "Binary permission setting failed", fields)
		return wrappedErr
	}

	// Copy static assets if present
	if buildResult.HasStatic && buildResult.StaticDir != "" {
		staticDir := filepath.Join(appDir, "static")
		if err := copyDir(buildResult.StaticDir, staticDir); err != nil {
			// Log but continue
			d.logger.Warn(timeoutCtx, "Failed to copy static assets",
				errors.WithField(fields, "error", err.Error()))
		} else {
			fields["static_dir"] = staticDir
		}
	}

	// Set up environment variables
	env := make(map[string]string)

	// Add default environment variables
	for k, v := range d.config.DefaultEnv {
		env[k] = v
	}

	// Set port
	port := buildResult.Port
	if port == 0 {
		port = d.config.DefaultPort
	}
	if app.Port != 0 {
		port = app.Port
	}
	env["PORT"] = strconv.Itoa(port)
	fields["port"] = port

	// Set app-specific env variables
	for _, e := range app.Environment {
		env[e.Key] = e.Value
	}

	// Set database path if app uses SQLite
	if buildResult.HasDatabase {
		dbPath := filepath.Join(dbDir, "app.db")
		env["DATABASE_URL"] = fmt.Sprintf("sqlite://%s", dbPath)
		fields["db_path"] = dbPath

		// Configure database backup if enabled
		if d.config.BackupDatabases && d.backup != nil {
			if err := d.backup.AddDatabase(appID, dbPath); err != nil {
				// Log but continue
				d.logger.Warn(timeoutCtx, "Failed to configure database backup",
					errors.WithField(fields, "error", err.Error()))
			}
		}
	}

	// Set HOME directory
	env["HOME"] = appDir

	// Convert env map to slice for supervisor
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	// Configure proxy
	if err := d.proxy.AddRoute(appID, app.Domain, port); err != nil {
		wrappedErr := errors.Wrap(err, "failed to configure proxy")
		d.logger.Error(timeoutCtx, wrappedErr, "Proxy configuration failed", fields)
		return wrappedErr
	}

	// Start the app
	if err := d.supervisor.StartApp(appID, appBinaryPath, envSlice); err != nil {
		wrappedErr := errors.Wrap(err, "failed to start app")
		d.logger.Error(timeoutCtx, wrappedErr, "App start failed", fields)

		// Try to clean up proxy route
		if cleanupErr := d.proxy.RemoveRoute(appID); cleanupErr != nil {
			d.logger.Warn(timeoutCtx, "Failed to clean up proxy route after failed deployment",
				errors.WithField(fields, "error", cleanupErr.Error()))
		}

		return wrappedErr
	}

	// Update app status in database
	app.Status = "running"
	app.LastDeploy = time.Now()
	app.UpdatedAt = time.Now()
	app.Port = port

	if err := d.database.UpdateApp(timeoutCtx, app); err != nil {
		// Log but continue - the app is running
		d.logger.Warn(timeoutCtx, "Failed to update app status in database",
			errors.WithField(fields, "error", err.Error()))
	}

	d.logger.Info(timeoutCtx, "Application deployed successfully", fields)
	return nil
}

// Undeploy removes a deployed application
func (d *Deployer) Undeploy(ctx context.Context, appID string) error {
	fields := errors.FieldMap{
		"app_id": appID,
	}

	d.logger.Info(ctx, "Undeploying application", fields)

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, d.config.DeployTimeout)
	defer cancel()

	// Get app details from database
	app, err := d.database.GetApp(timeoutCtx, appID)
	if err != nil {
		// If app doesn't exist in database, just log and continue
		if errors.Is(err, errors.ErrAppNotFound) {
			d.logger.Warn(timeoutCtx, "App not found in database", fields)
		} else {
			wrappedErr := errors.Wrap(err, "failed to get app details")
			d.logger.Error(timeoutCtx, wrappedErr, "App retrieval failed", fields)
			return wrappedErr
		}
	} else {
		fields["app_name"] = app.Name
		fields["domain"] = app.Domain
	}

	// Stop the app
	if err := d.supervisor.StopApp(appID); err != nil {
		// Log but continue
		d.logger.Warn(timeoutCtx, "Failed to stop app",
			errors.WithField(fields, "error", err.Error()))
	}

	// Remove proxy route
	if err := d.proxy.RemoveRoute(appID); err != nil {
		// Log but continue
		d.logger.Warn(timeoutCtx, "Failed to remove proxy route",
			errors.WithField(fields, "error", err.Error()))
	}

	// Remove database backup
	if d.backup != nil {
		if err := d.backup.RemoveDatabase(appID); err != nil {
			// Log but continue
			d.logger.Warn(timeoutCtx, "Failed to remove database backup",
				errors.WithField(fields, "error", err.Error()))
		}
	}

	// Clean up app directory
	appDir := filepath.Join(d.config.AppsDir, appID)
	if err := os.RemoveAll(appDir); err != nil {
		// Log but continue
		d.logger.Warn(timeoutCtx, "Failed to remove app directory",
			errors.WithField(fields, "error", err.Error()))
	}

	// If app exists in database, update its status
	if app != nil {
		app.Status = "undeployed"
		app.UpdatedAt = time.Now()

		if err := d.database.UpdateApp(timeoutCtx, app); err != nil {
			// Log but continue
			d.logger.Warn(timeoutCtx, "Failed to update app status in database",
				errors.WithField(fields, "error", err.Error()))
		}
	}

	d.logger.Info(timeoutCtx, "Application undeployed successfully", fields)
	return nil
}
