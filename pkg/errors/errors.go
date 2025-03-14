package errors

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"
)

// Common error types
var (
	// Application errors
	ErrAppNotFound       = errors.New("application not found")
	ErrAppAlreadyExists  = errors.New("application already exists")
	ErrAppNotRunning     = errors.New("application not running")
	ErrAppAlreadyRunning = errors.New("application already running")

	// Deployment errors
	ErrDeploymentFailed  = errors.New("deployment failed")
	ErrBuildFailed       = errors.New("build failed")
	ErrInvalidRepository = errors.New("invalid repository")

	// Database errors
	ErrDatabaseConnection = errors.New("database connection error")
	ErrRecordNotFound     = errors.New("record not found")
	ErrInvalidData        = errors.New("invalid data")

	// Configuration errors
	ErrConfigurationError = errors.New("configuration error")

	// Security errors
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")

	// System errors
	ErrSystemError         = errors.New("system error")
	ErrResourceUnavailable = errors.New("resource unavailable")
)

// FieldMap is a type alias for structured log fields
type FieldMap map[string]interface{}

// Logger provides a structured logging interface
type Logger interface {
	Error(ctx context.Context, err error, msg string, fields FieldMap)
	Info(ctx context.Context, msg string, fields FieldMap)
	Debug(ctx context.Context, msg string, fields FieldMap)
	Warn(ctx context.Context, msg string, fields FieldMap)
}

// StandardLogger implements Logger using the standard log package
type StandardLogger struct {
	logger *log.Logger
}

// NewStandardLogger creates a new StandardLogger
func NewStandardLogger(logger *log.Logger) *StandardLogger {
	return &StandardLogger{
		logger: logger,
	}
}

// Error logs an error message
func (l *StandardLogger) Error(ctx context.Context, err error, msg string, fields FieldMap) {
	l.log("ERROR", ctx, fmt.Sprintf("%s: %v", msg, err), fields)
}

// Info logs an informational message
func (l *StandardLogger) Info(ctx context.Context, msg string, fields FieldMap) {
	l.log("INFO", ctx, msg, fields)
}

// Debug logs a debug message
func (l *StandardLogger) Debug(ctx context.Context, msg string, fields FieldMap) {
	l.log("DEBUG", ctx, msg, fields)
}

// Warn logs a warning message
func (l *StandardLogger) Warn(ctx context.Context, msg string, fields FieldMap) {
	l.log("WARN", ctx, msg, fields)
}

// log is a helper function to format and write log messages
func (l *StandardLogger) log(level string, ctx context.Context, msg string, fields FieldMap) {
	// Create a copy of fields to avoid modifying the original
	logFields := make(FieldMap)
	for k, v := range fields {
		logFields[k] = v
	}

	// Add basic metadata
	logFields["timestamp"] = time.Now().Format(time.RFC3339)
	logFields["level"] = level

	// Add caller information
	_, file, line, ok := runtime.Caller(2)
	if ok {
		logFields["caller"] = fmt.Sprintf("%s:%d", file, line)
	}

	// Add request ID from context if available
	if requestID, ok := ctx.Value("requestID").(string); ok {
		logFields["request_id"] = requestID
	}

	// Format the log message
	l.logger.Printf("[%s] %s %+v", level, msg, logFields)
}

// WithField adds a field to the provided fields map
func WithField(fields FieldMap, key string, value interface{}) FieldMap {
	if fields == nil {
		fields = make(FieldMap)
	}
	fields[key] = value
	return fields
}

// WithError adds an error to the provided fields map
func WithError(fields FieldMap, err error) FieldMap {
	return WithField(fields, "error", err.Error())
}

// WithRequest adds request information to the provided fields map
func WithRequest(fields FieldMap, method, path string) FieldMap {
	fields = WithField(fields, "http_method", method)
	fields = WithField(fields, "http_path", path)
	return fields
}

// Wrap wraps an error with a message
func Wrap(err error, msg string) error {
	return fmt.Errorf("%s: %w", msg, err)
}

// Is reports whether any error in err's chain matches target
func Is(err, target error) bool {
	return errors.Is(err, target)
}

// As finds the first error in err's chain that matches target
func As(err error, target interface{}) bool {
	return errors.As(err, target)
}

// New creates a new error
func New(msg string) error {
	return errors.New(msg)
}
