package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/danbruder/skyline/pkg/errors"
)

// mockLogger for testing
type mockLogger struct {
	t *testing.T
}

func (m *mockLogger) Error(ctx context.Context, err error, msg string, fields errors.FieldMap) {
	m.t.Logf("[ERROR] %s: %v %v", msg, err, fields)
}

func (m *mockLogger) Info(ctx context.Context, msg string, fields errors.FieldMap) {
	m.t.Logf("[INFO] %s %v", msg, fields)
}

func (m *mockLogger) Debug(ctx context.Context, msg string, fields errors.FieldMap) {
	m.t.Logf("[DEBUG] %s %v", msg, fields)
}

func (m *mockLogger) Warn(ctx context.Context, msg string, fields errors.FieldMap) {
	m.t.Logf("[WARN] %s %v", msg, fields)
}

func newMockLogger(t *testing.T) errors.Logger {
	return &mockLogger{t: t}
}

func TestParseRepoName(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
		wantErr  bool
	}{
		{
			name:     "GitHub URL with .git suffix",
			url:      "https://github.com/username/repo.git",
			expected: "username-repo",
			wantErr:  false,
		},
		{
			name:     "GitHub URL without .git suffix",
			url:      "https://github.com/username/repo",
			expected: "username-repo",
			wantErr:  false,
		},
		{
			name:     "GitHub URL with subdirectory",
			url:      "https://github.com/username/repo/subdirectory",
			expected: "repo-subdirectory",
			wantErr:  false,
		},
		{
			name:     "Non-GitHub URL",
			url:      "https://gitlab.com/username/repo.git",
			expected: "repo",
			wantErr:  false,
		},
		{
			name:     "Invalid URL",
			url:      "",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRepoName(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRepoName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("parseRepoName() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGitHubFetcher_FetchSource(t *testing.T) {
	// Skip this test if there's no git command available
	_, err := os.Stat("/usr/bin/git")
	if os.IsNotExist(err) {
		t.Skip("Git command not available, skipping test")
	}

	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "source-fetcher-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create fetcher
	config := SourceFetchConfig{
		GitBinary: "git",
		SourceDir: tempDir,
	}
	fetcher := NewGitHubFetcher(config, newMockLogger(t))

	// Test with public repository
	ctx := context.Background()
	repoURL := "https://github.com/golang/example"
	branch := "master"
	commit := ""

	sourceDir, err := fetcher.FetchSource(ctx, repoURL, branch, commit)
	if err != nil {
		t.Fatalf("FetchSource() error = %v", err)
	}

	// Verify the source directory exists
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		t.Errorf("Source directory was not created: %v", err)
	}

	// Verify it's a git repository
	gitDir := filepath.Join(sourceDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Errorf("Not a git repository, .git directory missing: %v", err)
	}

	// Test cleanup
	if err := fetcher.CleanupSource(ctx, sourceDir); err != nil {
		t.Fatalf("CleanupSource() error = %v", err)
	}

	// Verify the source directory was removed
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Errorf("Source directory was not removed")
	}
}
