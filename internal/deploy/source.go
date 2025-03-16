package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/danbruder/skyline/pkg/errors"
)

// SourceFetcher defines the interface for fetching source code
type SourceFetcher interface {
	FetchSource(ctx context.Context, repo, branch, commit string) (string, error)
	CleanupSource(ctx context.Context, path string) error
}

// SourceFetchConfig contains configuration for the source fetcher
type SourceFetchConfig struct {
	GitBinary      string
	SourceDir      string
	FetchTimeout   time.Duration
	CleanupOnError bool
	GitHubToken    string
}

// GitHubFetcher implements SourceFetcher for GitHub repositories
type GitHubFetcher struct {
	config SourceFetchConfig
	logger errors.Logger
	mu     sync.Mutex
}

// NewGitHubFetcher creates a new GitHubFetcher
func NewGitHubFetcher(config SourceFetchConfig, logger errors.Logger) *GitHubFetcher {
	// Set defaults
	if config.GitBinary == "" {
		config.GitBinary = "git"
	}
	if config.SourceDir == "" {
		config.SourceDir = "data/source"
	}
	if config.FetchTimeout == 0 {
		config.FetchTimeout = 5 * time.Minute
	}

	return &GitHubFetcher{
		config: config,
		logger: logger,
	}
}

// FetchSource fetches source code from GitHub
func (g *GitHubFetcher) FetchSource(ctx context.Context, repoURL, branch, commit string) (string, error) {
	fields := errors.FieldMap{
		"repo_url": repoURL,
		"branch":   branch,
		"commit":   commit,
	}

	// Parse repo name from URL
	repoName, err := parseRepoName(repoURL)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse repository name")
	}
	fields["repo_name"] = repoName

	// Create source directory
	sourceDir := filepath.Join(g.config.SourceDir, repoName)
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		wrappedErr := errors.Wrap(err, "failed to create source directory")
		g.logger.Error(ctx, wrappedErr, "Source directory creation failed", fields)
		return "", wrappedErr
	}

	// Use mutex to prevent concurrent git operations on the same repo
	g.mu.Lock()
	defer g.mu.Unlock()

	// Check if repo is already cloned
	isCloned, err := g.isRepoCloned(sourceDir)
	if err != nil {
		wrappedErr := errors.Wrap(err, "failed to check if repo is cloned")
		g.logger.Error(ctx, wrappedErr, "Repo check failed", fields)
		return "", wrappedErr
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, g.config.FetchTimeout)
	defer cancel()

	// Clone or update repository
	if isCloned {
		g.logger.Info(ctx, "Updating existing repository", fields)
		if err := g.updateRepo(timeoutCtx, sourceDir, branch); err != nil {
			wrappedErr := errors.Wrap(err, "failed to update repository")
			g.logger.Error(ctx, wrappedErr, "Repository update failed", fields)

			if g.config.CleanupOnError {
				if cleanErr := g.CleanupSource(ctx, sourceDir); cleanErr != nil {
					g.logger.Error(ctx, cleanErr, "Cleanup after failed update failed", fields)
				}
			}
			return "", wrappedErr
		}
	} else {
		g.logger.Info(ctx, "Cloning new repository", fields)
		if err := g.cloneRepo(timeoutCtx, repoURL, sourceDir, branch); err != nil {
			wrappedErr := errors.Wrap(err, "failed to clone repository")
			g.logger.Error(ctx, wrappedErr, "Repository clone failed", fields)

			if g.config.CleanupOnError {
				if cleanErr := g.CleanupSource(ctx, sourceDir); cleanErr != nil {
					g.logger.Error(ctx, cleanErr, "Cleanup after failed clone failed", fields)
				}
			}
			return "", wrappedErr
		}
	}

	// Checkout specific commit if provided
	if commit != "" && commit != "HEAD" {
		g.logger.Info(ctx, "Checking out specific commit", fields)
		if err := g.checkoutCommit(timeoutCtx, sourceDir, commit); err != nil {
			wrappedErr := errors.Wrap(err, "failed to checkout commit")
			g.logger.Error(ctx, wrappedErr, "Commit checkout failed", fields)
			return "", wrappedErr
		}
	}

	g.logger.Info(ctx, "Source code fetched successfully", fields)
	return sourceDir, nil
}

// CleanupSource removes the source directory
func (g *GitHubFetcher) CleanupSource(ctx context.Context, path string) error {
	fields := errors.FieldMap{"path": path}

	g.logger.Info(ctx, "Cleaning up source directory", fields)
	if err := os.RemoveAll(path); err != nil {
		wrappedErr := errors.Wrap(err, "failed to remove source directory")
		g.logger.Error(ctx, wrappedErr, "Source cleanup failed", fields)
		return wrappedErr
	}

	return nil
}

// isRepoCloned checks if the repository is already cloned
func (g *GitHubFetcher) isRepoCloned(dir string) (bool, error) {
	gitDir := filepath.Join(dir, ".git")
	_, err := os.Stat(gitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// cloneRepo clones a repository
func (g *GitHubFetcher) cloneRepo(ctx context.Context, repoURL, dir, branch string) error {
	// Prepare command arguments
	args := []string{"clone"}

	// Add authentication if token is provided
	cloneURL := repoURL
	if g.config.GitHubToken != "" {
		// Insert token into URL
		if strings.HasPrefix(repoURL, "https://github.com/") {
			cloneURL = strings.Replace(repoURL, "https://", fmt.Sprintf("https://%s@", g.config.GitHubToken), 1)
		}
	}

	if branch != "" && branch != "main" && branch != "master" {
		args = append(args, "-b", branch)
	}

	args = append(args, cloneURL, dir)

	// Run git clone
	cmd := exec.CommandContext(ctx, g.config.GitBinary, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Mask token from error message for security
		sanitizedOutput := string(output)
		if g.config.GitHubToken != "" {
			sanitizedOutput = strings.Replace(sanitizedOutput, g.config.GitHubToken, "***", -1)
		}
		return fmt.Errorf("git clone failed: %w\nOutput: %s", err, sanitizedOutput)
	}

	return nil
}

// updateRepo updates a repository
func (g *GitHubFetcher) updateRepo(ctx context.Context, dir, branch string) error {
	// Make sure we're on the right branch
	if branch != "" {
		cmd := exec.CommandContext(ctx, g.config.GitBinary, "checkout", branch)
		cmd.Dir = dir
		if _, err := cmd.CombinedOutput(); err != nil {
			// Try to fetch and checkout
			fetchCmd := exec.CommandContext(ctx, g.config.GitBinary, "fetch", "origin")
			fetchCmd.Dir = dir
			if _, fetchErr := fetchCmd.CombinedOutput(); fetchErr != nil {
				return fmt.Errorf("git fetch failed: %w", fetchErr)
			}

			// Try checkout again after fetch
			checkoutCmd := exec.CommandContext(ctx, g.config.GitBinary, "checkout", branch)
			checkoutCmd.Dir = dir
			if checkoutOutput, checkoutErr := checkoutCmd.CombinedOutput(); checkoutErr != nil {
				return fmt.Errorf("git checkout failed: %w\nOutput: %s", checkoutErr, checkoutOutput)
			}
		}
	}

	// Pull latest changes
	cmd := exec.CommandContext(ctx, g.config.GitBinary, "pull", "origin", branch)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull failed: %w\nOutput: %s", err, output)
	}

	return nil
}

// checkoutCommit checks out a specific commit
func (g *GitHubFetcher) checkoutCommit(ctx context.Context, dir, commit string) error {
	cmd := exec.CommandContext(ctx, g.config.GitBinary, "checkout", commit)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout commit failed: %w\nOutput: %s", err, output)
	}

	return nil
}

// parseRepoName extracts the repository name from the URL
func parseRepoName(repoURL string) (string, error) {
	// Handle GitHub URLs
	// https://github.com/username/repo.git -> username-repo
	// https://github.com/username/repo -> username-repo
	if strings.Contains(repoURL, "github.com") {
		parts := strings.Split(repoURL, "/")
		if len(parts) < 2 {
			return "", fmt.Errorf("invalid GitHub URL format: %s", repoURL)
		}

		repoName := parts[len(parts)-1]
		userName := parts[len(parts)-2]

		// Remove .git suffix if present
		repoName = strings.TrimSuffix(repoName, ".git")

		return fmt.Sprintf("%s-%s", userName, repoName), nil
	}

	// Fallback for other git URLs or formats
	// Extract the last part of the path and remove .git
	parts := strings.Split(repoURL, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid URL format: %s", repoURL)
	}

	repoName := parts[len(parts)-1]
	repoName = strings.TrimSuffix(repoName, ".git")

	return repoName, nil
}
