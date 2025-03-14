package db

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/danbruder/skyline/pkg/errors"
	"github.com/google/uuid"
)

// App represents a deployed application
type App struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RepoURL     string    `json:"repo_url"`
	Branch      string    `json:"branch"`
	Domain      string    `json:"domain"`
	Port        int       `json:"port"`
	Status      string    `json:"status"` // pending, running, stopped, failed
	LastDeploy  time.Time `json:"last_deploy"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Environment []EnvVar  `json:"environment"`
}

// EnvVar represents an environment variable
type EnvVar struct {
	AppID string `json:"app_id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Deployment represents a deployment of an app
type Deployment struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	CommitSHA string    `json:"commit_sha"`
	Status    string    `json:"status"` // pending, success, failed
	Logs      string    `json:"logs"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// Backup represents a database backup
type Backup struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Status    string    `json:"status"` // pending, success, failed
	CreatedAt time.Time `json:"created_at"`
}

// Database handles database operations
type Database struct {
	sql    *SQL
	logger errors.Logger
}

// New creates a new database connection
func New(ctx context.Context, dbPath string, logger errors.Logger) (*Database, error) {
	sql, err := NewSQL(ctx, dbPath, logger)
	if err != nil {
		return nil, err
	}

	// Run migrations
	if err := sql.Migrate(ctx); err != nil {
		sql.Close()
		return nil, err
	}

	return &Database{
		sql:    sql,
		logger: logger,
	}, nil
}

// Close closes the database connection
func (d *Database) Close() error {
	return d.sql.Close()
}

// CreateApp creates a new app
func (d *Database) CreateApp(ctx context.Context, app *App) error {
	fields := errors.FieldMap{"app_name": app.Name, "repo_url": app.RepoURL}

	// Generate ID if not provided
	if app.ID == "" {
		app.ID = uuid.New().String()
	}

	// Set timestamps if not provided
	now := time.Now()
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = now
	}

	// Set default status if not provided
	if app.Status == "" {
		app.Status = "pending"
	}

	return d.sql.Transaction(ctx, func(tx *sql.Tx) error {
		// Insert app
		_, err := tx.ExecContext(ctx, `
			INSERT INTO apps (id, name, repo_url, branch, domain, port, status, last_deploy, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, app.ID, app.Name, app.RepoURL, app.Branch, app.Domain, app.Port, app.Status,
			app.LastDeploy, app.CreatedAt, app.UpdatedAt)

		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to insert app")
			d.logger.Error(ctx, wrappedErr, "App creation failed", fields)
			return wrappedErr
		}

		// Insert environment variables
		for _, env := range app.Environment {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO env_vars (app_id, key, value) VALUES (?, ?, ?)
			`, app.ID, env.Key, env.Value)

			if err != nil {
				wrappedErr := errors.Wrap(err, "failed to insert environment variable")
				d.logger.Error(ctx, wrappedErr, "Environment variable creation failed",
					errors.WithField(fields, "env_key", env.Key))
				return wrappedErr
			}
		}

		d.logger.Info(ctx, "App created successfully", errors.WithField(fields, "app_id", app.ID))

		return nil
	})
}

// GetApp retrieves an app by ID
func (d *Database) GetApp(ctx context.Context, id string) (*App, error) {
	fields := errors.FieldMap{"app_id": id}

	app := &App{ID: id}

	// Get app details
	row, err := d.sql.QueryRowContext(ctx, `
		SELECT name, repo_url, branch, domain, port, status, last_deploy, created_at, updated_at
		FROM apps WHERE id = ?
	`, id)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query app")
		d.logger.Error(ctx, wrappedErr, "App retrieval failed", fields)
		return nil, wrappedErr
	}

	err = row.Scan(
		&app.Name, &app.RepoURL, &app.Branch, &app.Domain, &app.Port,
		&app.Status, &app.LastDeploy, &app.CreatedAt, &app.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			wrappedErr := errors.Wrap(errors.ErrAppNotFound, "app not found in database")
			d.logger.Debug(ctx, "App not found", fields)
			return nil, wrappedErr
		}

		wrappedErr := errors.Wrap(err, "failed to scan app row")
		d.logger.Error(ctx, wrappedErr, "App data scan failed", fields)
		return nil, wrappedErr
	}

	// Get environment variables
	rows, err := d.sql.QueryContext(ctx, `
		SELECT key, value FROM env_vars WHERE app_id = ?
	`, id)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query environment variables")
		d.logger.Error(ctx, wrappedErr, "Environment variables retrieval failed", fields)
		return nil, wrappedErr
	}
	defer rows.Close()

	for rows.Next() {
		var env EnvVar
		env.AppID = id

		if err := rows.Scan(&env.Key, &env.Value); err != nil {
			wrappedErr := errors.Wrap(err, "failed to scan environment variable")
			d.logger.Error(ctx, wrappedErr, "Environment variable scan failed", fields)
			return nil, wrappedErr
		}

		app.Environment = append(app.Environment, env)
	}

	if err := rows.Err(); err != nil {
		wrappedErr := errors.Wrap(err, "error iterating environment variables")
		d.logger.Error(ctx, wrappedErr, "Environment variables iteration failed", fields)
		return nil, wrappedErr
	}

	d.logger.Debug(ctx, "App retrieved successfully", fields)
	return app, nil
}

// ListApps lists all apps
func (d *Database) ListApps(ctx context.Context) ([]*App, error) {
	fields := errors.FieldMap{}

	// Query apps
	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, name, repo_url, branch, domain, port, status, last_deploy, created_at, updated_at
		FROM apps ORDER BY name
	`)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query apps")
		d.logger.Error(ctx, wrappedErr, "Apps listing failed", fields)
		return nil, wrappedErr
	}
	defer rows.Close()

	apps := make([]*App, 0)
	appIDs := make([]string, 0)

	for rows.Next() {
		app := &App{}

		if err := rows.Scan(
			&app.ID, &app.Name, &app.RepoURL, &app.Branch, &app.Domain, &app.Port,
			&app.Status, &app.LastDeploy, &app.CreatedAt, &app.UpdatedAt,
		); err != nil {
			wrappedErr := errors.Wrap(err, "failed to scan app row")
			d.logger.Error(ctx, wrappedErr, "App scan failed", fields)
			return nil, wrappedErr
		}

		apps = append(apps, app)
		appIDs = append(appIDs, app.ID)
	}

	if err := rows.Err(); err != nil {
		wrappedErr := errors.Wrap(err, "error iterating apps")
		d.logger.Error(ctx, wrappedErr, "Apps iteration failed", fields)
		return nil, wrappedErr
	}

	// Get environment variables for all apps
	if len(apps) > 0 {
		envMap := make(map[string][]EnvVar)

		// Prepare placeholders for IN clause
		placeholders := make([]string, len(appIDs))
		args := make([]interface{}, len(appIDs))

		for i, id := range appIDs {
			placeholders[i] = "?"
			args[i] = id
		}

		query := "SELECT app_id, key, value FROM env_vars WHERE app_id IN (" +
			strings.Join(placeholders, ",") + ")"

		envRows, err := d.sql.QueryContext(ctx, query, args...)
		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to query environment variables")
			d.logger.Error(ctx, wrappedErr, "Environment variables retrieval failed", fields)
			return nil, wrappedErr
		}
		defer envRows.Close()

		for envRows.Next() {
			var env EnvVar

			if err := envRows.Scan(&env.AppID, &env.Key, &env.Value); err != nil {
				wrappedErr := errors.Wrap(err, "failed to scan environment variable")
				d.logger.Error(ctx, wrappedErr, "Environment variable scan failed", fields)
				return nil, wrappedErr
			}

			envMap[env.AppID] = append(envMap[env.AppID], env)
		}

		if err := envRows.Err(); err != nil {
			wrappedErr := errors.Wrap(err, "error iterating environment variables")
			d.logger.Error(ctx, wrappedErr, "Environment variables iteration failed", fields)
			return nil, wrappedErr
		}

		// Assign environment variables to apps
		for _, app := range apps {
			if envs, ok := envMap[app.ID]; ok {
				app.Environment = envs
			}
		}
	}

	d.logger.Debug(ctx, "Apps listed successfully", errors.WithField(fields, "count", len(apps)))
	return apps, nil
}

// UpdateApp updates an app
func (d *Database) UpdateApp(ctx context.Context, app *App) error {
	fields := errors.FieldMap{"app_id": app.ID, "app_name": app.Name}

	// Update timestamp
	app.UpdatedAt = time.Now()

	return d.sql.Transaction(ctx, func(tx *sql.Tx) error {
		// Update app
		result, err := tx.ExecContext(ctx, `
			UPDATE apps SET name = ?, repo_url = ?, branch = ?, domain = ?, 
			port = ?, status = ?, last_deploy = ?, updated_at = ?
			WHERE id = ?
		`, app.Name, app.RepoURL, app.Branch, app.Domain, app.Port,
			app.Status, app.LastDeploy, app.UpdatedAt, app.ID)

		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to update app")
			d.logger.Error(ctx, wrappedErr, "App update failed", fields)
			return wrappedErr
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to get rows affected")
			d.logger.Error(ctx, wrappedErr, "Rows affected check failed", fields)
			return wrappedErr
		}

		if rowsAffected == 0 {
			wrappedErr := errors.Wrap(errors.ErrAppNotFound, "app not found")
			d.logger.Debug(ctx, "App not found for update", fields)
			return wrappedErr
		}

		// Delete existing environment variables
		_, err = tx.ExecContext(ctx, "DELETE FROM env_vars WHERE app_id = ?", app.ID)
		if err != nil {
			wrappedErr := errors.Wrap(err, "failed to delete environment variables")
			d.logger.Error(ctx, wrappedErr, "Environment variables deletion failed", fields)
			return wrappedErr
		}

		// Insert new environment variables
		for _, env := range app.Environment {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO env_vars (app_id, key, value) VALUES (?, ?, ?)
			`, app.ID, env.Key, env.Value)

			if err != nil {
				wrappedErr := errors.Wrap(err, "failed to insert environment variable")
				d.logger.Error(ctx, wrappedErr, "Environment variable creation failed",
					errors.WithField(fields, "env_key", env.Key))
				return wrappedErr
			}
		}

		d.logger.Info(ctx, "App updated successfully", fields)
		return nil
	})
}

// DeleteApp deletes an app
func (d *Database) DeleteApp(ctx context.Context, id string) error {
	fields := errors.FieldMap{"app_id": id}

	result, err := d.sql.ExecContext(ctx, "DELETE FROM apps WHERE id = ?", id)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to delete app")
		d.logger.Error(ctx, wrappedErr, "App deletion failed", fields)
		return wrappedErr
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to get rows affected")
		d.logger.Error(ctx, wrappedErr, "Rows affected check failed", fields)
		return wrappedErr
	}

	if rowsAffected == 0 {
		wrappedErr := errors.Wrap(errors.ErrAppNotFound, "app not found")
		d.logger.Debug(ctx, "App not found for deletion", fields)
		return wrappedErr
	}

	d.logger.Info(ctx, "App deleted successfully", fields)
	return nil
}

// CreateDeployment creates a new deployment
func (d *Database) CreateDeployment(ctx context.Context, deployment *Deployment) error {
	fields := errors.FieldMap{"app_id": deployment.AppID, "deployment_id": deployment.ID}

	// Generate ID if not provided
	if deployment.ID == "" {
		deployment.ID = uuid.New().String()
		fields["deployment_id"] = deployment.ID
	}

	// Set timestamps if not provided
	if deployment.StartedAt.IsZero() {
		deployment.StartedAt = time.Now()
	}

	// Set default status if not provided
	if deployment.Status == "" {
		deployment.Status = "pending"
	}

	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO deployments (id, app_id, commit_sha, status, logs, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, deployment.ID, deployment.AppID, deployment.CommitSHA, deployment.Status,
		deployment.Logs, deployment.StartedAt, deployment.EndedAt)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to insert deployment")
		d.logger.Error(ctx, wrappedErr, "Deployment creation failed", fields)
		return wrappedErr
	}

	d.logger.Info(ctx, "Deployment created successfully", fields)
	return nil
}

// UpdateDeployment updates a deployment
func (d *Database) UpdateDeployment(ctx context.Context, deployment *Deployment) error {
	fields := errors.FieldMap{"deployment_id": deployment.ID, "app_id": deployment.AppID}

	result, err := d.sql.ExecContext(ctx, `
		UPDATE deployments SET commit_sha = ?, status = ?, logs = ?, ended_at = ?
		WHERE id = ?
	`, deployment.CommitSHA, deployment.Status, deployment.Logs, deployment.EndedAt, deployment.ID)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to update deployment")
		d.logger.Error(ctx, wrappedErr, "Deployment update failed", fields)
		return wrappedErr
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to get rows affected")
		d.logger.Error(ctx, wrappedErr, "Rows affected check failed", fields)
		return wrappedErr
	}

	if rowsAffected == 0 {
		wrappedErr := errors.Wrap(errors.ErrRecordNotFound, "deployment not found")
		d.logger.Debug(ctx, "Deployment not found for update", fields)
		return wrappedErr
	}

	d.logger.Debug(ctx, "Deployment updated successfully", fields)
	return nil
}

// GetDeployment retrieves a deployment by ID
func (d *Database) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	fields := errors.FieldMap{"deployment_id": id}

	row, err := d.sql.QueryRowContext(ctx, `
		SELECT app_id, commit_sha, status, logs, started_at, ended_at
		FROM deployments WHERE id = ?
	`, id)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query deployment")
		d.logger.Error(ctx, wrappedErr, "Deployment retrieval failed", fields)
		return nil, wrappedErr
	}

	deployment := &Deployment{ID: id}

	err = row.Scan(
		&deployment.AppID, &deployment.CommitSHA, &deployment.Status,
		&deployment.Logs, &deployment.StartedAt, &deployment.EndedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			wrappedErr := errors.Wrap(errors.ErrRecordNotFound, "deployment not found")
			d.logger.Debug(ctx, "Deployment not found", fields)
			return nil, wrappedErr
		}

		wrappedErr := errors.Wrap(err, "failed to scan deployment row")
		d.logger.Error(ctx, wrappedErr, "Deployment data scan failed", fields)
		return nil, wrappedErr
	}

	d.logger.Debug(ctx, "Deployment retrieved successfully", fields)
	return deployment, nil
}

// ListDeployments lists all deployments for an app
func (d *Database) ListDeployments(ctx context.Context, appID string) ([]*Deployment, error) {
	fields := errors.FieldMap{"app_id": appID}

	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, commit_sha, status, logs, started_at, ended_at
		FROM deployments WHERE app_id = ? ORDER BY started_at DESC
	`, appID)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query deployments")
		d.logger.Error(ctx, wrappedErr, "Deployments listing failed", fields)
		return nil, wrappedErr
	}
	defer rows.Close()

	deployments := make([]*Deployment, 0)

	for rows.Next() {
		deployment := &Deployment{AppID: appID}

		if err := rows.Scan(
			&deployment.ID, &deployment.CommitSHA, &deployment.Status,
			&deployment.Logs, &deployment.StartedAt, &deployment.EndedAt,
		); err != nil {
			wrappedErr := errors.Wrap(err, "failed to scan deployment row")
			d.logger.Error(ctx, wrappedErr, "Deployment scan failed", fields)
			return nil, wrappedErr
		}

		deployments = append(deployments, deployment)
	}

	if err := rows.Err(); err != nil {
		wrappedErr := errors.Wrap(err, "error iterating deployments")
		d.logger.Error(ctx, wrappedErr, "Deployments iteration failed", fields)
		return nil, wrappedErr
	}

	d.logger.Debug(ctx, "Deployments listed successfully",
		errors.WithField(fields, "count", len(deployments)))
	return deployments, nil
}

// CreateBackup creates a new backup
func (d *Database) CreateBackup(ctx context.Context, backup *Backup) error {
	fields := errors.FieldMap{"app_id": backup.AppID, "backup_id": backup.ID}

	// Generate ID if not provided
	if backup.ID == "" {
		backup.ID = uuid.New().String()
		fields["backup_id"] = backup.ID
	}

	// Set timestamps if not provided
	if backup.CreatedAt.IsZero() {
		backup.CreatedAt = time.Now()
	}

	// Set default status if not provided
	if backup.Status == "" {
		backup.Status = "pending"
	}

	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO backups (id, app_id, path, size, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, backup.ID, backup.AppID, backup.Path, backup.Size, backup.Status, backup.CreatedAt)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to insert backup")
		d.logger.Error(ctx, wrappedErr, "Backup creation failed", fields)
		return wrappedErr
	}

	d.logger.Info(ctx, "Backup created successfully", fields)
	return nil
}

// ListBackups lists all backups for an app
func (d *Database) ListBackups(ctx context.Context, appID string) ([]*Backup, error) {
	fields := errors.FieldMap{"app_id": appID}

	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, path, size, status, created_at
		FROM backups WHERE app_id = ? ORDER BY created_at DESC
	`, appID)

	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to query backups")
		d.logger.Error(ctx, wrappedErr, "Backups listing failed", fields)
		return nil, wrappedErr
	}
	defer rows.Close()

	backups := make([]*Backup, 0)

	for rows.Next() {
		backup := &Backup{AppID: appID}

		if err := rows.Scan(
			&backup.ID, &backup.Path, &backup.Size, &backup.Status, &backup.CreatedAt,
		); err != nil {
			wrappedErr := errors.Wrap(err, "failed to scan backup row")
			d.logger.Error(ctx, wrappedErr, "Backup scan failed", fields)
			return nil, wrappedErr
		}

		backups = append(backups, backup)
	}

	if err := rows.Err(); err != nil {
		wrappedErr := errors.Wrap(err, "error iterating backups")
		d.logger.Error(ctx, wrappedErr, "Backups iteration failed", fields)
		return nil, wrappedErr
	}

	d.logger.Debug(ctx, "Backups listed successfully",
		errors.WithField(fields, "count", len(backups)))
	return backups, nil
}
