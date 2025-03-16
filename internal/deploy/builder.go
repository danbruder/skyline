package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/danbruder/skyline/pkg/errors"
)

// AppBuilder defines the interface for building applications
type AppBuilder interface {
	DetectAndBuild(ctx context.Context, sourceDir, outputDir string) (BuildResult, error)
}

// BuildResult contains information about the built application
type BuildResult struct {
	Type        string            // go, rust, etc.
	BinaryPath  string            // Path to the built binary
	Environment map[string]string // Environment variables needed to run the app
	Port        int               // Default port the app listens on
	HasDatabase bool              // Whether the app uses a database
	HasStatic   bool              // Whether the app has static assets
	StaticDir   string            // Path to static assets directory
}

// BuildConfig contains configuration for the builder
type BuildConfig struct {
	GoBinary      string
	RustBinary    string
	CargoBinary   string
	BuildTimeout  time.Duration
	OutputDir     string
	EnableCaching bool
	EnvVars       map[string]string
}

// Builder implements AppBuilder
type Builder struct {
	config BuildConfig
	logger errors.Logger
}

// NewBuilder creates a new Builder
func NewBuilder(config BuildConfig, logger errors.Logger) *Builder {
	// Set defaults
	if config.GoBinary == "" {
		config.GoBinary = "go"
	}
	if config.RustBinary == "" {
		config.RustBinary = "rustc"
	}
	if config.CargoBinary == "" {
		config.CargoBinary = "cargo"
	}
	if config.BuildTimeout == 0 {
		config.BuildTimeout = 10 * time.Minute
	}
	if config.OutputDir == "" {
		config.OutputDir = "data/builds"
	}
	if config.EnvVars == nil {
		config.EnvVars = make(map[string]string)
	}

	return &Builder{
		config: config,
		logger: logger,
	}
}

// DetectAndBuild detects the application type and builds it
func (b *Builder) DetectAndBuild(ctx context.Context, sourceDir, outputID string) (BuildResult, error) {
	fields := errors.FieldMap{
		"source_dir": sourceDir,
		"output_id":  outputID,
	}

	b.logger.Info(ctx, "Detecting application type", fields)

	// Create output directory
	outputDir := filepath.Join(b.config.OutputDir, outputID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to create output directory")
		b.logger.Error(ctx, wrappedErr, "Output directory creation failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, b.config.BuildTimeout)
	defer cancel()

	// Detect application type
	if isGoApp(sourceDir) {
		b.logger.Info(ctx, "Detected Go application", fields)
		return b.buildGoApp(timeoutCtx, sourceDir, outputDir)
	} else if isRustApp(sourceDir) {
		b.logger.Info(ctx, "Detected Rust application", fields)
		return b.buildRustApp(timeoutCtx, sourceDir, outputDir)
	}

	err := errors.New("unsupported application type")
	b.logger.Error(ctx, err, "Application type detection failed", fields)
	return BuildResult{}, err
}

// buildGoApp builds a Go application
func (b *Builder) buildGoApp(ctx context.Context, sourceDir, outputDir string) (BuildResult, error) {
	fields := errors.FieldMap{
		"source_dir": sourceDir,
		"output_dir": outputDir,
		"type":       "go",
	}

	// Set up build environment
	env := os.Environ()
	for k, v := range b.config.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	// Add CGO_ENABLED=0 for static linking
	env = append(env, "CGO_ENABLED=0")
	// Clean Go modules cache to ensure fresh builds
	env = append(env, "GOCACHE="+filepath.Join(outputDir, ".cache"))

	// Find main.go files
	mainFiles, err := findGoMainFiles(sourceDir)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to find Go main package")
		b.logger.Error(ctx, wrappedErr, "Go main package detection failed", fields)
		return BuildResult{}, wrappedErr
	}

	if len(mainFiles) == 0 {
		err := errors.New("no Go main package found")
		b.logger.Error(ctx, err, "Go main package detection failed", fields)
		return BuildResult{}, err
	}

	// Use the first main.go found
	mainFile := mainFiles[0]
	mainDir := filepath.Dir(mainFile)
	relMainDir, err := filepath.Rel(sourceDir, mainDir)
	if err != nil {
		relMainDir = mainDir // Fallback to absolute path
	}

	fields["main_dir"] = relMainDir
	b.logger.Info(ctx, "Building Go application", fields)

	// Determine output binary name
	outputBinaryName := filepath.Base(outputDir)
	outputBinaryPath := filepath.Join(outputDir, outputBinaryName)

	// Determine if the app has a database
	hasDB, _ := detectSQLiteUsage(sourceDir)
	fields["has_database"] = hasDB

	// Find default port
	port, _ := detectDefaultPort(sourceDir)
	fields["port"] = port

	// Check for static assets
	hasStatic, staticDir := detectStaticAssets(sourceDir)
	fields["has_static"] = hasStatic

	// Run go build
	cmd := exec.CommandContext(ctx, b.config.GoBinary, "build", "-o", outputBinaryPath)
	cmd.Dir = mainDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		wrappedErr := errors.Wrap(err, fmt.Sprintf("go build failed: %s", output))
		b.logger.Error(ctx, wrappedErr, "Go build failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Make binary executable
	if err := os.Chmod(outputBinaryPath, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to make binary executable")
		b.logger.Error(ctx, wrappedErr, "Binary permission setting failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Copy static assets if present
	if hasStatic && staticDir != "" {
		destStaticDir := filepath.Join(outputDir, "static")
		if err := copyDir(staticDir, destStaticDir); err != nil {
			b.logger.Warn(ctx, "Failed to copy static assets", errors.WithField(fields, "error", err.Error()))
			// Continue without static assets
			hasStatic = false
		} else {
			staticDir = destStaticDir
		}
	}

	result := BuildResult{
		Type:        "go",
		BinaryPath:  outputBinaryPath,
		Environment: make(map[string]string),
		Port:        port,
		HasDatabase: hasDB,
		HasStatic:   hasStatic,
		StaticDir:   staticDir,
	}

	b.logger.Info(ctx, "Go application built successfully", fields)
	return result, nil
}

// buildRustApp builds a Rust application
func (b *Builder) buildRustApp(ctx context.Context, sourceDir, outputDir string) (BuildResult, error) {
	fields := errors.FieldMap{
		"source_dir": sourceDir,
		"output_dir": outputDir,
		"type":       "rust",
	}

	// Set up build environment
	env := os.Environ()
	for k, v := range b.config.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	// Add rustup target for static linking
	env = append(env, "RUSTFLAGS=-C target-feature=+crt-static")

	// Check for Cargo.toml
	cargoPath := filepath.Join(sourceDir, "Cargo.toml")
	if _, err := os.Stat(cargoPath); os.IsNotExist(err) {
		err := errors.New("Cargo.toml not found")
		b.logger.Error(ctx, err, "Rust project detection failed", fields)
		return BuildResult{}, err
	}

	b.logger.Info(ctx, "Building Rust application", fields)

	// Determine output binary name
	outputBinaryName := filepath.Base(outputDir)
	outputBinaryPath := filepath.Join(outputDir, outputBinaryName)

	// Determine if the app has a database
	hasDB, _ := detectSQLiteUsage(sourceDir)
	fields["has_database"] = hasDB

	// Find default port
	port, _ := detectDefaultPort(sourceDir)
	fields["port"] = port

	// Check for static assets
	hasStatic, staticDir := detectStaticAssets(sourceDir)
	fields["has_static"] = hasStatic

	// Run cargo build
	cmd := exec.CommandContext(ctx, b.config.CargoBinary, "build", "--release")
	cmd.Dir = sourceDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		wrappedErr := errors.Wrap(err, fmt.Sprintf("cargo build failed: %s", output))
		b.logger.Error(ctx, wrappedErr, "Rust build failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Find the binary in target/release directory
	releaseBinary := findRustReleaseBinary(sourceDir)
	if releaseBinary == "" {
		err := errors.New("could not find Rust release binary")
		b.logger.Error(ctx, err, "Rust binary not found", fields)
		return BuildResult{}, err
	}

	// Copy binary to output directory
	if err := copyFile(releaseBinary, outputBinaryPath); err != nil {
		wrappedErr := errors.Wrap(err, "failed to copy Rust binary")
		b.logger.Error(ctx, wrappedErr, "Binary copy failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Make binary executable
	if err := os.Chmod(outputBinaryPath, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to make binary executable")
		b.logger.Error(ctx, wrappedErr, "Binary permission setting failed", fields)
		return BuildResult{}, wrappedErr
	}

	// Copy static assets if present
	if hasStatic && staticDir != "" {
		destStaticDir := filepath.Join(outputDir, "static")
		if err := copyDir(staticDir, destStaticDir); err != nil {
			b.logger.Warn(ctx, "Failed to copy static assets", errors.WithField(fields, "error", err.Error()))
			// Continue without static assets
			hasStatic = false
		} else {
			staticDir = destStaticDir
		}
	}

	result := BuildResult{
		Type:        "rust",
		BinaryPath:  outputBinaryPath,
		Environment: make(map[string]string),
		Port:        port,
		HasDatabase: hasDB,
		HasStatic:   hasStatic,
		StaticDir:   staticDir,
	}

	b.logger.Info(ctx, "Rust application built successfully", fields)
	return result, nil
}

// Helper functions

// isGoApp checks if the directory contains a Go application
func isGoApp(dir string) bool {
	// Check for go.mod
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return true
	}

	// Check for .go files
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	return err == nil && len(matches) > 0
}

// isRustApp checks if the directory contains a Rust application
func isRustApp(dir string) bool {
	// Check for Cargo.toml
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return true
	}

	// Check for .rs files
	matches, err := filepath.Glob(filepath.Join(dir, "*.rs"))
	return err == nil && len(matches) > 0
}

// findGoMainFiles finds all main.go files in the project
func findGoMainFiles(dir string) ([]string, error) {
	mainFiles := []string{}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}

		// Check for main.go files
		if !info.IsDir() && info.Name() == "main.go" {
			// Read file to check for package main
			content, err := os.ReadFile(path)
			if err != nil {
				return nil // Skip files we can't read
			}

			if strings.Contains(string(content), "package main") {
				mainFiles = append(mainFiles, path)
			}
		}

		return nil
	})

	return mainFiles, err
}

// findRustReleaseBinary finds the release binary in target/release
func findRustReleaseBinary(dir string) string {
	releaseDir := filepath.Join(dir, "target", "release")
	binaries, err := filepath.Glob(filepath.Join(releaseDir, "*"))
	if err != nil {
		return ""
	}

	for _, binary := range binaries {
		info, err := os.Stat(binary)
		if err != nil {
			continue
		}

		// Check if file is executable and not a directory
		mode := info.Mode()
		if !info.IsDir() && mode&0111 != 0 {
			return binary
		}
	}

	// Also check for the binary with the same name as the project
	projectName := filepath.Base(dir)
	projectBinary := filepath.Join(releaseDir, projectName)
	if _, err := os.Stat(projectBinary); err == nil {
		return projectBinary
	}

	return ""
}

// detectSQLiteUsage checks if the app uses SQLite
func detectSQLiteUsage(dir string) (bool, error) {
	// For Go, check imports
	goFiles, err := filepath.Glob(filepath.Join(dir, "**/*.go"))
	if err == nil {
		for _, file := range goFiles {
			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}

			// Check for SQLite imports
			if strings.Contains(string(content), "github.com/mattn/go-sqlite3") ||
				strings.Contains(string(content), "modernc.org/sqlite") {
				return true, nil
			}
		}
	}

	// For Rust, check Cargo.toml
	cargoPath := filepath.Join(dir, "Cargo.toml")
	if _, err := os.Stat(cargoPath); err == nil {
		content, err := os.ReadFile(cargoPath)
		if err == nil {
			// Check for SQLite dependencies
			if strings.Contains(string(content), "rusqlite") ||
				strings.Contains(string(content), "sqlite") {
				return true, nil
			}
		}
	}

	return false, nil
}

// detectDefaultPort tries to detect the default port the app listens on
func detectDefaultPort(dir string) (int, error) {
	// Default port if we can't detect
	defaultPort := 8080

	// For Go, check for common patterns
	goFiles, err := filepath.Glob(filepath.Join(dir, "**/*.go"))
	if err == nil {
		for _, file := range goFiles {
			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}

			// Check for common port patterns
			contentStr := string(content)
			if strings.Contains(contentStr, "ListenAndServe(\":") ||
				strings.Contains(contentStr, "Listen(\":") {
				// Try to extract port
				return extractPort(contentStr)
			}
		}
	}

	// For Rust, check for common patterns
	rustFiles, err := filepath.Glob(filepath.Join(dir, "**/*.rs"))
	if err == nil {
		for _, file := range rustFiles {
			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}

			// Check for common port patterns
			contentStr := string(content)
			if strings.Contains(contentStr, ".bind(") ||
				strings.Contains(contentStr, "listen(") {
				// Try to extract port
				return extractPort(contentStr)
			}
		}
	}

	return defaultPort, nil
}

// extractPort tries to extract a port number from code
func extractPort(content string) (int, error) {
	// Common patterns for port definition
	patterns := []string{
		"ListenAndServe\\(\":[0-9]+",
		"Listen\\(\":[0-9]+",
		"\\.bind\\(\"[^\"]*:[0-9]+",
		"listen\\(\"[^\"]*:[0-9]+",
		"PORT[^=]*=[^0-9]*[0-9]+",
		"port[^=]*=[^0-9]*[0-9]+",
	}

	for _, pattern := range patterns {
		// Find matches
		portStr := findPortInPattern(content, pattern)
		if portStr != "" {
			// Convert to integer
			var port int
			if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil {
				return port, nil
			}
		}
	}

	// Default port if not found
	return 8080, nil
}

// findPortInPattern extracts a port number from text matching a pattern
func findPortInPattern(content, pattern string) string {
	index := strings.Index(content, pattern)
	if index == -1 {
		return ""
	}

	// Extract the matched text
	matched := content[index:]
	endIndex := strings.IndexAny(matched, ",)};\n")
	if endIndex != -1 {
		matched = matched[:endIndex]
	}

	// Find the port number
	for i := len(matched) - 1; i >= 0; i-- {
		if matched[i] >= '0' && matched[i] <= '9' {
			// Found a digit, backtrack to find the start of the number
			end := i + 1
			for i >= 0 && matched[i] >= '0' && matched[i] <= '9' {
				i--
			}
			return matched[i+1 : end]
		}
	}

	return ""
}

// detectStaticAssets checks for static assets directory
func detectStaticAssets(dir string) (bool, string) {
	// Common static asset directory names
	staticDirs := []string{"static", "public", "assets", "dist", "www"}

	for _, staticDir := range staticDirs {
		path := filepath.Join(dir, staticDir)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return true, path
		}
	}

	return false, ""
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceContent, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, sourceContent, 0644)
}

// copyDir recursively copies a directory from src to dst
func copyDir(src, dst string) error {
	// Get source info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory
	if err = os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	// Read directory entries
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	// Copy each entry
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectory
			if err = copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Copy file
			if err = copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}
