package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/danbruder/skyline/internal/db"
	"github.com/danbruder/skyline/pkg/errors"
	"github.com/danbruder/skyline/pkg/events"
	"github.com/google/uuid"
)

// PipelineConfig contains configuration for the deployment pipeline
type PipelineConfig struct {
	SourceDir string
	BuildDir  string
	Timeout   time.Duration
}

// Pipeline orchestrates the deployment process
type Pipeline struct {
	config   PipelineConfig
	logger   errors.Logger
	database *db.Database
	eventBus *events.EventBus
	fetcher  SourceFetcher
	builder  AppBuilder
	deployer AppDeployer
}

// NewPipeline creates a new deployment pipeline
func NewPipeline(
	config PipelineConfig,
	logger errors.Logger,
	database *db.Database,
	eventBus *events.EventBus,
	fetcher SourceFetcher,
	builder AppBuilder,
	deployer AppDeployer,
) *Pipeline {
	// Set defaults
	if config.SourceDir == "" {
		config.SourceDir = "data/source"
	}
	if config.BuildDir == "" {
		config.BuildDir = "data/builds"
	}
	if config.Timeout == 0 {
		config.Timeout = 15 * time.Minute
	}

	return &Pipeline{
		config:   config,
		logger:   logger,
		database: database,
		eventBus: eventBus,
		fetcher:  fetcher,
		builder:  builder,
		deployer: deployer,
	}
}

// DeployApp handles the full deployment process
func (p *Pipeline) DeployApp(ctx context.Context, appID, commit string) error {
	fields := errors.FieldMap{
		"app_id": appID,
		"commit": commit,
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	p.logger.Info(timeoutCtx, "Starting deployment pipeline", fields)

	// Get app from database
	app, err := p.database.GetApp(timeoutCtx, appID)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to get app details")
		p.logger.Error(timeoutCtx, wrappedErr, "App retrieval failed", fields)
		return wrappedErr
	}

	fields["app_name"] = app.Name
	fields["repo_url"] = app.RepoURL
	fields["branch"] = app.Branch

	// Create deployment record
	deployID := uuid.New().String()
	deployment := &db.Deployment{
		ID:        deployID,
		AppID:     appID,
		CommitSHA: commit,
		Status:    "pending",
		StartedAt: time.Now(),
	}

	if err := p.database.CreateDeployment(timeoutCtx, deployment); err != nil {
		wrappedErr := errors.Wrap(err, "failed to create deployment record")
		p.logger.Error(timeoutCtx, wrappedErr, "Deployment record creation failed", fields)
		return wrappedErr
	}

	fields["deployment_id"] = deployID

	// Publish deployment started event
	p.eventBus.Publish(events.Event{
		Type:    events.AppDeployed,
		AppID:   appID,
		Message: fmt.Sprintf("Starting deployment of app %s", app.Name),
		Data: map[string]interface{}{
			"deployment_id": deployID,
			"commit":        commit,
		},
	})

	// Update deployment status
	updateDeployment := func(status, logs string) {
		deployment.Status = status
		deployment.Logs = logs
		deployment.EndedAt = time.Now()

		if err := p.database.UpdateDeployment(timeoutCtx, deployment); err != nil {
			p.logger.Warn(timeoutCtx, "Failed to update deployment record",
				errors.WithField(fields, "error", err.Error()))
		}
	}

	// Step 1: Fetch source code
	p.logger.Info(timeoutCtx, "Fetching source code", fields)

	sourceDir, err := p.fetcher.FetchSource(timeoutCtx, app.RepoURL, app.Branch, commit)
	if err != nil {
		wrappedErr := errors.Wrap(err, "source fetching failed")
		p.logger.Error(timeoutCtx, wrappedErr, "Source fetching failed", fields)

		updateDeployment("failed", fmt.Sprintf("Source fetching failed: %v", err))

		p.eventBus.Publish(events.Event{
			Type:    events.AppFailed,
			AppID:   appID,
			Message: fmt.Sprintf("Deployment of app %s failed: source fetching error", app.Name),
			Data: map[string]interface{}{
				"deployment_id": deployID,
				"error":         err.Error(),
			},
		})

		return wrappedErr
	}

	// Step 2: Build application
	p.logger.Info(timeoutCtx, "Building application", fields)

	buildResult, err := p.builder.DetectAndBuild(timeoutCtx, sourceDir, appID)
	if err != nil {
		wrappedErr := errors.Wrap(err, "build failed")
		p.logger.Error(timeoutCtx, wrappedErr, "Build failed", fields)

		updateDeployment("failed", fmt.Sprintf("Build failed: %v", err))

		p.eventBus.Publish(events.Event{
			Type:    events.AppFailed,
			AppID:   appID,
			Message: fmt.Sprintf("Deployment of app %s failed: build error", app.Name),
			Data: map[string]interface{}{
				"deployment_id": deployID,
				"error":         err.Error(),
			},
		})

		return wrappedErr
	}

	fields["app_type"] = buildResult.Type

	// Step 3: Deploy application
	p.logger.Info(timeoutCtx, "Deploying application", fields)

	if err := p.deployer.Deploy(timeoutCtx, buildResult, appID); err != nil {
		wrappedErr := errors.Wrap(err, "deployment failed")
		p.logger.Error(timeoutCtx, wrappedErr, "Deployment failed", fields)

		updateDeployment("failed", fmt.Sprintf("Deployment failed: %v", err))

		p.eventBus.Publish(events.Event{
			Type:    events.AppFailed,
			AppID:   appID,
			Message: fmt.Sprintf("Deployment of app %s failed: deployment error", app.Name),
			Data: map[string]interface{}{
				"deployment_id": deployID,
				"error":         err.Error(),
			},
		})

		return wrappedErr
	}

	// Update deployment record as successful
	updateDeployment("success", "Deployment completed successfully")

	// Publish deployment completed event
	p.eventBus.Publish(events.Event{
		Type:    events.AppDeployed,
		AppID:   appID,
		Message: fmt.Sprintf("Successfully deployed app %s", app.Name),
		Data: map[string]interface{}{
			"deployment_id": deployID,
			"commit":        commit,
		},
	})

	p.logger.Info(timeoutCtx, "Deployment pipeline completed successfully", fields)
	return nil
}

// UndeployApp handles the full undeployment process
func (p *Pipeline) UndeployApp(ctx context.Context, appID string) error {
	fields := errors.FieldMap{
		"app_id": appID,
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	p.logger.Info(timeoutCtx, "Starting undeployment process", fields)

	// Get app from database
	app, err := p.database.GetApp(timeoutCtx, appID)
	if err != nil {
		// If app doesn't exist in database, just log and continue
		if errors.Is(err, errors.ErrAppNotFound) {
			p.logger.Warn(timeoutCtx, "App not found in database", fields)
		} else {
			wrappedErr := errors.Wrap(err, "failed to get app details")
			p.logger.Error(timeoutCtx, wrappedErr, "App retrieval failed", fields)
			return wrappedErr
		}
	} else {
		fields["app_name"] = app.Name
	}

	// Publish undeployment started event
	appName := appID
	if app != nil {
		appName = app.Name
	}
	p.eventBus.Publish(events.Event{
		Type:    events.AppStopped,
		AppID:   appID,
		Message: fmt.Sprintf("Starting undeployment of app %s", appName),
	})

	// Undeploy the app
	if err := p.deployer.Undeploy(timeoutCtx, appID); err != nil {
		wrappedErr := errors.Wrap(err, "undeployment failed")
		p.logger.Error(timeoutCtx, wrappedErr, "Undeployment failed", fields)

		p.eventBus.Publish(events.Event{
			Type:    events.AppFailed,
			AppID:   appID,
			Message: fmt.Sprintf("Undeployment of app %s failed", appName),
			Data: map[string]interface{}{
				"error": err.Error(),
			},
		})

		return wrappedErr
	}

	// Publish undeployment completed event
	p.eventBus.Publish(events.Event{
		Type:    events.AppStopped,
		AppID:   appID,
		Message: fmt.Sprintf("Successfully undeployed app %s", appName),
	})

	p.logger.Info(timeoutCtx, "Undeployment process completed successfully", fields)
	return nil
}

// ProcessWebhook processes a GitHub webhook event
func (p *Pipeline) ProcessWebhook(ctx context.Context, event WebhookEvent) error {
	fields := errors.FieldMap{
		"event_type": event.Type,
		"repo_url":   event.RepoURL,
		"branch":     event.Branch,
		"commit":     event.CommitSHA,
	}

	p.logger.Info(ctx, "Processing GitHub webhook event", fields)

	// Find apps using this repository and branch
	apps, err := p.database.ListApps(ctx)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to list apps")
		p.logger.Error(ctx, wrappedErr, "App listing failed", fields)
		return wrappedErr
	}

	matchingApps := 0
	for _, app := range apps {
		// Check if repo and branch match
		if app.RepoURL == event.RepoURL && app.Branch == event.Branch {
			appFields := errors.WithField(fields, "app_id", app.ID)
			appFields = errors.WithField(appFields, "app_name", app.Name)

			p.logger.Info(ctx, "Found matching app for webhook event", appFields)

			// Trigger deployment in a goroutine
			go func(appID, commit string) {
				deployCtx := context.Background()
				if err := p.DeployApp(deployCtx, appID, commit); err != nil {
					p.logger.Error(deployCtx, err, "Webhook-triggered deployment failed",
						errors.FieldMap{
							"app_id":       appID,
							"commit":       commit,
							"webhook_type": event.Type,
						})
				}
			}(app.ID, event.CommitSHA)

			matchingApps++
		}
	}

	fields["matching_apps"] = matchingApps
	p.logger.Info(ctx, "Webhook processing completed", fields)
	return nil
}

// WebhookEvent contains information about a GitHub webhook event
type WebhookEvent struct {
	Type      string // push, pull_request, etc.
	RepoURL   string // Repository URL
	Branch    string // Branch name
	CommitSHA string // Commit SHA
}
