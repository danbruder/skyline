package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the main configuration
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	API        APIConfig        `yaml:"api"`
	Proxy      ProxyConfig      `yaml:"proxy"`
	Supervisor SupervisorConfig `yaml:"supervisor"`
	Backup     BackupConfig     `yaml:"backup"`
	GitHub     GitHubConfig     `yaml:"github"`
}

// ServerConfig contains server configuration
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// DatabaseConfig contains database configuration
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// APIConfig contains API configuration
type APIConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	UIDir        string        `yaml:"ui_dir"`
}

// ProxyConfig contains proxy configuration
type ProxyConfig struct {
	CaddyPath     string        `yaml:"caddy_path"`
	ConfigPath    string        `yaml:"config_path"`
	TemplateFile  string        `yaml:"template_file"`
	AdminAPIPort  int           `yaml:"admin_api_port"`
	AdminAPIAddr  string        `yaml:"admin_api_addr"`
	ReloadTimeout time.Duration `yaml:"reload_timeout"`
}

// SupervisorConfig contains supervisor configuration
type SupervisorConfig struct {
	AppsDir      string        `yaml:"apps_dir"`
	MaxRestarts  int           `yaml:"max_restarts"`
	RestartDelay time.Duration `yaml:"restart_delay"`
}

// BackupConfig contains backup configuration
type BackupConfig struct {
	LitestreamPath    string `yaml:"litestream_path"`
	LitestreamConfig  string `yaml:"litestream_config"`
	BackupDestination string `yaml:"backup_destination"`
	S3Bucket          string `yaml:"s3_bucket"`
	S3Region          string `yaml:"s3_region"`
	S3Endpoint        string `yaml:"s3_endpoint"`
	S3AccessKeyID     string `yaml:"s3_access_key_id"`
	S3AccessKey       string `yaml:"s3_access_key"`
	SyncInterval      string `yaml:"sync_interval"`
	RetentionPolicy   string `yaml:"retention_policy"`
}

// GitHubConfig contains GitHub configuration
type GitHubConfig struct {
	WebhookSecret string `yaml:"webhook_secret"`
}

// Load loads configuration from a file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	if config.Server.Port == 0 {
		config.Server.Port = 8080
	}
	if config.API.Port == 0 {
		config.API.Port = 8080
	}
	if config.Database.Path == "" {
		config.Database.Path = "data/system/deploy-platform.db"
	}
	if config.Supervisor.AppsDir == "" {
		config.Supervisor.AppsDir = "data/apps"
	}
	if config.Supervisor.MaxRestarts == 0 {
		config.Supervisor.MaxRestarts = 5
	}
	if config.Supervisor.RestartDelay == 0 {
		config.Supervisor.RestartDelay = 5 * time.Second
	}
	if config.Proxy.CaddyPath == "" {
		config.Proxy.CaddyPath = "caddy"
	}
	if config.Proxy.ConfigPath == "" {
		config.Proxy.ConfigPath = "data/system/caddy.json"
	}
	if config.Proxy.AdminAPIPort == 0 {
		config.Proxy.AdminAPIPort = 2019
	}
	if config.Backup.LitestreamPath == "" {
		config.Backup.LitestreamPath = "litestream"
	}
	if config.Backup.LitestreamConfig == "" {
		config.Backup.LitestreamConfig = "data/system/litestream.yml"
	}
	if config.Backup.SyncInterval == "" {
		config.Backup.SyncInterval = "10s"
	}
	if config.Backup.RetentionPolicy == "" {
		config.Backup.RetentionPolicy = "24h"
	}

	return config, nil
}

// Save saves configuration to a file
func Save(config *Config, path string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
