package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danbruder/skyline/pkg/errors"
	_ "github.com/mattn/go-sqlite3"
)

// SQL represents a SQLite database
type SQL struct {
	db     *sql.DB
	logger errors.Logger
}

// NewSQL creates a new SQLite database
func NewSQL(ctx context.Context, dbPath string, logger errors.Logger) (*SQL, error) {
	fields := errors.FieldMap{"db_path": dbPath}

	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to create database directory")
		logger.Error(ctx, wrappedErr, "Database directory creation failed", fields)
		return nil, wrappedErr
	}

	// Connect with WAL mode and foreign key support
	connStr := dbPath + "?_journal=WAL&_foreign_keys=on"
	db, err := sql.Open("sqlite3", connStr)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to open database")
		logger.Error(ctx, wrappedErr, "Database connection failed", fields)
		return nil, wrappedErr
	}

	// Set connection options
	db.SetMaxOpenConns(1) // SQLite can only handle one writer at a time
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)

	// Test connection
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		wrappedErr := errors.Wrap(err, "failed to ping database")
		logger.Error(ctx, wrappedErr, "Database ping failed", fields)
		return nil, wrappedErr
	}

	logger.Info(ctx, "Database connection established", fields)
	return &SQL{
		db:     db,
		logger: logger,
	}, nil
}

// Close closes the database connection
func (s *SQL) Close() error {
	ctx := context.Background()
	fields := errors.FieldMap{}

	if err := s.db.Close(); err != nil {
		wrappedErr := errors.Wrap(err, "failed to close database")
		s.logger.Error(ctx, wrappedErr, "Database close failed", fields)
		return wrappedErr
	}

	s.logger.Debug(ctx, "Database connection closed", fields)
	return nil
}

// Migrate runs database migrations
func (s *SQL) Migrate(ctx context.Context) error {
	fields := errors.FieldMap{}
	s.logger.Info(ctx, "Running database migrations", fields)

	// Begin transaction for migrations
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to begin transaction")
		s.logger.Error(ctx, wrappedErr, "Migration transaction failed", fields)
		return wrappedErr
	}

	// Create apps table
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS apps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			repo_url TEXT NOT NULL,
			branch TEXT NOT NULL,
			domain TEXT NOT NULL,
			port INTEGER NOT NULL,
			status TEXT NOT NULL,
			last_deploy TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create apps table")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Create env_vars table
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS env_vars (
			app_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY (app_id, key),
			FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create env_vars table")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Create deployments table
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS deployments (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			commit_sha TEXT NOT NULL,
			status TEXT NOT NULL,
			logs TEXT,
			started_at TIMESTAMP NOT NULL,
			ended_at TIMESTAMP,
			FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create deployments table")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Create backups table
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS backups (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			path TEXT NOT NULL,
			size INTEGER NOT NULL,
			status TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create backups table")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Create index on app_id in deployments for faster lookups
	_, err = tx.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create index")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Create index on app_id in backups for faster lookups
	_, err = tx.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_backups_app_id ON backups(app_id)
	`)
	if err != nil {
		tx.Rollback()
		wrappedErr := errors.Wrap(err, "failed to create index")
		s.logger.Error(ctx, wrappedErr, "Migration failed", fields)
		return wrappedErr
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		wrappedErr := errors.Wrap(err, "failed to commit transaction")
		s.logger.Error(ctx, wrappedErr, "Migration commit failed", fields)
		return wrappedErr
	}

	s.logger.Info(ctx, "Database migrations completed successfully", fields)
	return nil
}

// Transaction executes a function within a transaction
func (s *SQL) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	fields := errors.FieldMap{}

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to begin transaction")
		s.logger.Error(ctx, wrappedErr, "Transaction start failed", fields)
		return wrappedErr
	}

	// Execute function
	if err := fn(tx); err != nil {
		// Try to roll back
		if rbErr := tx.Rollback(); rbErr != nil {
			rollbackErr := errors.Wrap(rbErr, "failed to rollback transaction")
			s.logger.Error(ctx, rollbackErr, "Transaction rollback failed", fields)
			// Return original error wrapped with rollback error
			return fmt.Errorf("%v (rollback failed: %v)", err, rbErr)
		}
		return err
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		wrappedErr := errors.Wrap(err, "failed to commit transaction")
		s.logger.Error(ctx, wrappedErr, "Transaction commit failed", fields)
		return wrappedErr
	}

	return nil
}

// QueryRowContext executes a query that returns a single row
func (s *SQL) QueryRowContext(ctx context.Context, query string, args ...interface{}) (*sql.Row, error) {
	fields := errors.FieldMap{
		"query": cleanQueryForLog(query),
		"args":  args,
	}

	s.logger.Debug(ctx, "Executing query", fields)
	return s.db.QueryRowContext(ctx, query, args...), nil
}

// QueryContext executes a query that returns rows
func (s *SQL) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	fields := errors.FieldMap{
		"query": cleanQueryForLog(query),
		"args":  args,
	}

	s.logger.Debug(ctx, "Executing query", fields)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		wrappedErr := errors.Wrap(err, "query execution failed")
		s.logger.Error(ctx, wrappedErr, "Query failed", fields)
		return nil, wrappedErr
	}

	return rows, nil
}

// ExecContext executes a query that doesn't return rows
func (s *SQL) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	fields := errors.FieldMap{
		"query": cleanQueryForLog(query),
		"args":  args,
	}

	s.logger.Debug(ctx, "Executing statement", fields)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		wrappedErr := errors.Wrap(err, "statement execution failed")
		s.logger.Error(ctx, wrappedErr, "Statement failed", fields)
		return nil, wrappedErr
	}

	return result, nil
}

// Helper method to clean query strings for logging
func cleanQueryForLog(query string) string {
	// Replace newlines with spaces and collapse multiple spaces
	query = strings.ReplaceAll(query, "\n", " ")
	for strings.Contains(query, "  ") {
		query = strings.ReplaceAll(query, "  ", " ")
	}
	return query
}
