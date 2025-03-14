package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/danbruder/skyline/internal/config"
)

// RouteConfig represents a Caddy route configuration
type RouteConfig struct {
	Domain    string
	TargetURL string
}

// CaddyManager manages Caddy configuration
type CaddyManager struct {
	cfg      config.ProxyConfig
	logger   *log.Logger
	cmd      *exec.Cmd
	routes   map[string]RouteConfig
	mu       sync.RWMutex
	caddyAPI string
}

// NewCaddyManager creates a new Caddy manager
func NewCaddyManager(cfg config.ProxyConfig, logger *log.Logger) *CaddyManager {
	if cfg.AdminAPIAddr == "" {
		cfg.AdminAPIAddr = "localhost"
	}
	if cfg.AdminAPIPort == 0 {
		cfg.AdminAPIPort = 2019
	}
	if cfg.ReloadTimeout == 0 {
		cfg.ReloadTimeout = 10 * time.Second
	}

	return &CaddyManager{
		cfg:      cfg,
		logger:   logger,
		routes:   make(map[string]RouteConfig),
		caddyAPI: fmt.Sprintf("http://%s:%d", cfg.AdminAPIAddr, cfg.AdminAPIPort),
	}
}

// Start starts Caddy
func (c *CaddyManager) Start() error {
	c.logger.Println("Starting Caddy...")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(c.cfg.ConfigPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create initial config
	if err := c.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	// Start Caddy
	cmd := exec.Command(c.cfg.CaddyPath, "run", "--config", c.cfg.ConfigPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Caddy: %w", err)
	}
	c.cmd = cmd

	// Wait for Caddy to start
	time.Sleep(1 * time.Second)

	// Test Caddy API
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(c.caddyAPI)
	if err != nil {
		c.logger.Printf("Warning: Caddy API not reachable: %v", err)
	} else {
		resp.Body.Close()
	}

	return nil
}

// Stop stops Caddy
func (c *CaddyManager) Stop() error {
	c.logger.Println("Stopping Caddy...")

	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}

	if err := c.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill Caddy process: %w", err)
	}

	return nil
}

// AddRoute adds a route to Caddy
func (c *CaddyManager) AddRoute(appID, domain string, port int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.routes[appID] = RouteConfig{
		Domain:    domain,
		TargetURL: fmt.Sprintf("http://localhost:%d", port),
	}

	return c.reloadConfig()
}

// RemoveRoute removes a route from Caddy
func (c *CaddyManager) RemoveRoute(appID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.routes, appID)
	return c.reloadConfig()
}

// ListRoutes lists all routes
func (c *CaddyManager) ListRoutes() map[string]RouteConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	routes := make(map[string]RouteConfig)
	for k, v := range c.routes {
		routes[k] = v
	}

	return routes
}

// Private methods

func (c *CaddyManager) generateConfig() error {
	// Load template if provided
	var configTemplate map[string]interface{}
	if c.cfg.TemplateFile != "" {
		data, err := os.ReadFile(c.cfg.TemplateFile)
		if err != nil {
			return fmt.Errorf("failed to read template file: %w", err)
		}

		if err := json.Unmarshal(data, &configTemplate); err != nil {
			return fmt.Errorf("failed to parse template file: %w", err)
		}
	} else {
		// Use default template
		configTemplate = map[string]interface{}{
			"admin": map[string]interface{}{
				"listen": fmt.Sprintf("%s:%d", c.cfg.AdminAPIAddr, c.cfg.AdminAPIPort),
			},
			"apps": map[string]interface{}{
				"http": map[string]interface{}{
					"servers": map[string]interface{}{
						"main": map[string]interface{}{
							"listen": [1]string{":80"},
							"routes": []interface{}{},
						},
					},
				},
			},
		}
	}

	// Add routes
	servers := configTemplate["apps"].(map[string]interface{})["http"].(map[string]interface{})["servers"].(map[string]interface{})
	mainServer := servers["main"].(map[string]interface{})
	routes := make([]interface{}, 0)

	for _, route := range c.routes {
		routes = append(routes, map[string]interface{}{
			"match": []interface{}{
				map[string]interface{}{
					"host": []string{route.Domain},
				},
			},
			"handle": []interface{}{
				map[string]interface{}{
					"handler": "reverse_proxy",
					"upstreams": []interface{}{
						map[string]interface{}{
							"dial": route.TargetURL,
						},
					},
				},
			},
		})
	}

	mainServer["routes"] = routes

	// Save config
	configJSON, err := json.MarshalIndent(configTemplate, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(c.cfg.ConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (c *CaddyManager) reloadConfig() error {
	// Generate new config
	if err := c.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	// If Caddy is not running, we're done
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}

	// Reload via API
	configData, err := os.ReadFile(c.cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	client := &http.Client{Timeout: c.cfg.ReloadTimeout}
	req, err := http.NewRequest("POST", c.caddyAPI+"/load", bytes.NewReader(configData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to reload config: status %d", resp.StatusCode)
	}

	c.logger.Println("Caddy config reloaded successfully")
	return nil
}
