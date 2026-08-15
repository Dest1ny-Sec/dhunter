// Package config loads Dhunter server configuration from a YAML file.
//
// The file is intentionally small: server bind, agent sidecar URL, MCP
// endpoint, LLM credentials, and storage path. Defaults are applied for
// any missing field so a fresh checkout can boot with `dhunter-server`
// and a placeholder YAML.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level Dhunter server configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Agent   AgentConfig   `yaml:"agent"`
	MCP     MCPConfig     `yaml:"mcp"`
	LLM     LLMConfig     `yaml:"llm"`
	Storage StorageConfig `yaml:"storage"`
	Admin   AdminConfig   `yaml:"admin"`
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	Port                int      `yaml:"port"`
	Host                string   `yaml:"host"`
	SSEKeepaliveSeconds int      `yaml:"sse_keepalive_seconds"`
	// AllowedOrigins restricts CORS. Empty = same-origin only (the default
	// single-port deployment needs no CORS at all). Set e.g.
	// ["https://ui.example.com"] for a separate frontend host.
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// AgentConfig points to the Python sidecar that runs the LLM loop.
type AgentConfig struct {
	PythonURL string `yaml:"python_url"`
}

// MCPConfig holds tool / transport wiring for MCP servers.
type MCPConfig struct {
	WebHunter WebHunterConfig `yaml:"webhunter"`
}

// WebHunterConfig points at the WebHunter MCP endpoint.
type WebHunterConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// LLMConfig carries upstream model credentials. We do not log API keys.
type LLMConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKey    string `yaml:"api_key"`
	BaseURL   string `yaml:"base_url"`
	MaxTokens int    `yaml:"max_tokens"`
}

// StorageConfig points at the on-disk SQLite database.
type StorageConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

// AdminConfig controls first-run admin bootstrap and the bearer token used
// by the API middleware. Username defaults to "admin" and is auto-generated
// (together with a random password) on first run — the user can change both
// later from the Settings page.
type AdminConfig struct {
	BootstrapPassword string `yaml:"bootstrap_password"`
	Username          string `yaml:"username"`
	Token             string `yaml:"token"`
}

// Default returns sane defaults so an empty YAML is still bootable.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port:                8080,
			Host:                "127.0.0.1",
			SSEKeepaliveSeconds: 15,
			AllowedOrigins:      []string{},
		},
		Agent: AgentConfig{
			PythonURL: "http://127.0.0.1:9100",
		},
		MCP: MCPConfig{
			WebHunter: WebHunterConfig{
				URL:   "http://127.0.0.1:9124/message",
				Token: "dhunter-mcp-please-change-me",
			},
		},
		LLM: LLMConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-5",
			BaseURL:   "https://api.minimaxi.com/anthropic",
			APIKey:    "",
			MaxTokens: 32768,
		},
		Storage: StorageConfig{
			SQLitePath: "./data/dhunter.db",
		},
		Admin: AdminConfig{
			BootstrapPassword: "",
			Username:          "admin",
			Token:             "dhunter-admin-please-change-me",
		},
	}
}

// Load reads YAML from path and merges with defaults. Missing fields fall
// back to defaults; invalid port/env overrides surface as errors.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = "./configs/dhunter.yaml"
	}

	// Anchor for relative storage paths: the directory containing the config
	// file, so `sqlite_path: ../data/dhunter.db` resolves the same way no
	// matter where the server was started from. This fixes both the old
	// "DB path drift" (relative path resolved against a moving cwd) and the
	// hardcoded macOS path in the shipped config. Stays empty when no config
	// file exists — then relative paths fall back to cwd (legacy behavior).
	anchorDir := ""

	if data, err := os.ReadFile(path); err == nil {
		// We unmarshal into cfg so missing keys keep their defaults.
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		if absDir, aerr := filepath.Abs(filepath.Dir(path)); aerr == nil {
			anchorDir = absDir
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	applyEnvOverrides(cfg)
	cfg.normalize(anchorDir)
	return cfg, nil
}

// applyEnvOverrides lets ops override the most-flipped knobs from the
// environment without rewriting the YAML on every deploy.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("DHUNTER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = n
		}
	}
	if v := os.Getenv("DHUNTER_AGENT_URL"); v != "" {
		cfg.Agent.PythonURL = v
	}
	if v := os.Getenv("DHUNTER_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("DHUNTER_SQLITE_PATH"); v != "" {
		cfg.Storage.SQLitePath = v
	}
	if v := os.Getenv("DHUNTER_ADMIN_TOKEN"); v != "" {
		cfg.Admin.Token = v
	}
	if v := os.Getenv("DHUNTER_ADMIN_BOOTSTRAP_PASSWORD"); v != "" {
		cfg.Admin.BootstrapPassword = v
	}
}

// normalize fills in any zero-valued fields the YAML forgot and resolves
// the SQLite path to an absolute path. A relative sqlite_path is anchored to
// the config file's directory (anchorDir); with no config file it falls back
// to the process cwd (legacy behavior).
func (c *Config) normalize(anchorDir string) {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.SSEKeepaliveSeconds == 0 {
		c.Server.SSEKeepaliveSeconds = 15
	}
	if c.Agent.PythonURL == "" {
		c.Agent.PythonURL = "http://127.0.0.1:9100"
	}
	if c.MCP.WebHunter.URL == "" {
		c.MCP.WebHunter.URL = "http://127.0.0.1:9124/message"
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 32768
	}
	if c.Storage.SQLitePath == "" {
		c.Storage.SQLitePath = "./data/dhunter.db"
	}
	if c.Admin.Token == "" {
		c.Admin.Token = "dhunter-admin-please-change-me"
	}
	if c.Admin.Username == "" {
		c.Admin.Username = "admin"
	}
	if !filepath.IsAbs(c.Storage.SQLitePath) {
		if anchorDir != "" {
			c.Storage.SQLitePath = filepath.Join(anchorDir, c.Storage.SQLitePath)
		} else if abs, err := filepath.Abs(c.Storage.SQLitePath); err == nil {
			c.Storage.SQLitePath = abs
		}
	}
}

// KeepAlive returns the SSE heartbeat interval as a time.Duration.
func (c *Config) KeepAlive() time.Duration {
	return time.Duration(c.Server.SSEKeepaliveSeconds) * time.Second
}
