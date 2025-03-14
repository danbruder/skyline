package backup

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/danbruder/skyline/pkg/events"
	"gopkg.in/yaml.v3"
)

// LitestreamConfig represents the configuration for Litestream
type LitestreamConfig struct {
	DBPath      string   `yaml:"-"`
	Addr        string   `yaml:"addr,omitempty"`
	Exec        []string `yaml:"exec,omitempty"`
	AccessKeyID string   `yaml:"access-key-id,omitempty"`
	AccessKey   string   `yaml:"access-key,omitempty"`
	Dbs         []DbConfig
}

// DbConfig represents a database configuration for Litestream
type DbConfig struct {
	Path     string          `yaml:"path"`
	Replicas []ReplicaConfig `yaml:"replicas"`
}

// ReplicaConfig represents a replica configuration for Litestream
type ReplicaConfig struct {
	Type            string `yaml:"type"`
	URL             string `yaml:"url"`
	Path            string `yaml:"path,omitempty"`
	Bucket          string `yaml:"bucket,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`
	Region          string `yaml:"region,omitempty"`
	AccessKeyID     string `yaml:"access-key-id,omitempty"`
	AccessKey       string `yaml:"secret-access-key,omitempty"`
	SkipVerify      bool   `yaml:"skip-verify,omitempty"`
	SyncInterval    string `yaml:"sync-interval,omitempty"`
	RetentionPolicy string `yaml:"retention,omitempty"`
}

// BackupConfig contains configuration for the backup manager
type BackupConfig struct {
	LitestreamPath    string
	LitestreamConfig  string
	BackupDestination string
	S3Bucket          string
	S3Region          string
	S3Endpoint        string
	S3AccessKeyID     string
	S3AccessKey       string
	SyncInterval      string
	RetentionPolicy   string
}

// BackupManager manages database backups
type BackupManager struct {
	cfg      BackupConfig
	logger   *log.Logger
	eventBus *events.EventBus
	cmd      *exec.Cmd
	mu       sync.RWMutex
	dbs      map[string]string // appID -> db path
}

// NewBackupManager creates a new backup manager
func NewBackupManager(cfg BackupConfig, logger *log.Logger, eventBus *events.EventBus) *BackupManager {
	if cfg.SyncInterval == "" {
		cfg.SyncInterval = "10s"
	}
	if cfg.RetentionPolicy == "" {
		cfg.RetentionPolicy = "24h"
	}

	return &BackupManager{
		cfg:      cfg,
		logger:   logger,
		eventBus: eventBus,
		dbs:      make(map[string]string),
	}
}

// Start starts the backup manager
func (b *BackupManager) Start() error {
	b.logger.Println("Starting backup manager...")

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(b.cfg.LitestreamConfig)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Generate initial config
	if err := b.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	// Start Litestream
	b.cmd = exec.Command(b.cfg.LitestreamPath, "replicate", "-config", b.cfg.LitestreamConfig)
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Litestream: %w", err)
	}

	return nil
}

// Stop stops the backup manager
func (b *BackupManager) Stop() error {
	b.logger.Println("Stopping backup manager...")

	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}

	if err := b.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill Litestream process: %w", err)
	}

	return nil
}

// AddDatabase adds a database to Litestream
func (b *BackupManager) AddDatabase(appID, dbPath string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.dbs[appID] = dbPath
	return b.reloadConfig()
}

// RemoveDatabase removes a database from Litestream
func (b *BackupManager) RemoveDatabase(appID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.dbs, appID)
	return b.reloadConfig()
}

// RestoreDatabase restores a database from backup
func (b *BackupManager) RestoreDatabase(appID, dbPath string) error {
	// Ensure Litestream knows about the database first
	if err := b.AddDatabase(appID, dbPath); err != nil {
		return err
	}

	// Stop Litestream temporarily
	if err := b.Stop(); err != nil {
		return err
	}

	// Restore the database
	cmd := exec.Command(
		b.cfg.LitestreamPath,
		"restore",
		"-config", b.cfg.LitestreamConfig,
		"-o", dbPath,
		dbPath,
	)
	if err := cmd.Run(); err != nil {
		// Try to restart Litestream
		b.Start()
		return fmt.Errorf("failed to restore database: %w", err)
	}

	// Restart Litestream
	return b.Start()
}

// Private methods

func (b *BackupManager) generateConfig() error {
	config := LitestreamConfig{
		DBPath:      b.cfg.LitestreamConfig,
		AccessKeyID: b.cfg.S3AccessKeyID,
		AccessKey:   b.cfg.S3AccessKey,
	}

	// Add databases
	for _, dbPath := range b.dbs {
		appName := filepath.Base(filepath.Dir(dbPath))
		dbConfig := DbConfig{
			Path: dbPath,
			Replicas: []ReplicaConfig{
				{
					Type:            "s3",
					Bucket:          b.cfg.S3Bucket,
					Path:            filepath.Join(b.cfg.BackupDestination, appName),
					Region:          b.cfg.S3Region,
					Endpoint:        b.cfg.S3Endpoint,
					AccessKeyID:     b.cfg.S3AccessKeyID,
					AccessKey:       b.cfg.S3AccessKey,
					SyncInterval:    b.cfg.SyncInterval,
					RetentionPolicy: b.cfg.RetentionPolicy,
				},
			},
		}
		config.Dbs = append(config.Dbs, dbConfig)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(b.cfg.LitestreamConfig, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (b *BackupManager) reloadConfig() error {
	// Generate new config
	if err := b.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	// If Litestream is not running, we're done
	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}

	// Restart Litestream
	if err := b.Stop(); err != nil {
		return err
	}

	return b.Start()
}
