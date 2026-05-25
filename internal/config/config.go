package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Database DatabaseConfig `toml:"database"`
	Privacy  PrivacyConfig  `toml:"privacy"`
	Import   ImportConfig   `toml:"import"`
	Cleanup  CleanupConfig  `toml:"cleanup"`
	Reports  ReportsConfig  `toml:"reports"`
	Agents   AgentsConfig   `toml:"agents"`
}

type DatabaseConfig struct {
	Path string `toml:"path"`
}

type PrivacyConfig struct {
	Mode                string `toml:"mode"`
	RedactPathsOnExport bool   `toml:"redact_paths_on_export"`
}

type ImportConfig struct {
	GracingMinutes int  `toml:"gracing_minutes"`
	SingleThread   bool `toml:"single_thread"`
}

type CleanupConfig struct {
	DefaultMode    string `toml:"default_mode"`
	OlderThanDays  int    `toml:"older_than_days"`
	PurgeAfterDays int    `toml:"purge_after_days"`
}

type ReportsConfig struct {
	Timezone string `toml:"timezone"`
	Currency string `toml:"currency"`
}

type AgentsConfig struct {
	Claude AgentConfig `toml:"claude"`
	Codex  AgentConfig `toml:"codex"`
	Gemini AgentConfig `toml:"gemini"`
	Qwen   AgentConfig `toml:"qwen"`
}

type AgentConfig struct {
	Enabled bool     `toml:"enabled"`
	Paths   []string `toml:"paths"`
}

func Default() *Config {
	return &Config{
		Database: DatabaseConfig{
			Path: filepath.Join(DataDir(), "agent-ledger.db"),
		},
		Privacy: PrivacyConfig{
			Mode:                "envelope",
			RedactPathsOnExport: true,
		},
		Import: ImportConfig{
			GracingMinutes: 15,
			SingleThread:   false,
		},
		Cleanup: CleanupConfig{
			DefaultMode:    "quarantine",
			OlderThanDays:  30,
			PurgeAfterDays: 90,
		},
		Reports: ReportsConfig{
			Timezone: "UTC",
			Currency: "USD",
		},
		Agents: AgentsConfig{
			Claude: AgentConfig{Enabled: true, Paths: []string{"~/.claude"}},
			Codex:  AgentConfig{Enabled: true, Paths: []string{"~/.codex"}},
			Gemini: AgentConfig{Enabled: true, Paths: []string{"~/.gemini"}},
			Qwen:   AgentConfig{Enabled: true, Paths: []string{"~/.qwen"}},
		},
	}
}

func ConfigPath() string {
	return filepath.Join(DataDir(), "config.toml")
}

func DataDir() string {
	if explicit := strings.TrimSpace(os.Getenv("AGENT_LEDGER_DATA_DIR")); explicit != "" {
		return ExpandHome(explicit)
	}

	if root := detectProjectRoot(); root != "" {
		return filepath.Join(root, "local", "data")
	}

	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".local", "share", "agent-ledger")
	}
	return filepath.Join(".", "local", "data")
}

func detectProjectRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		if root := findAncestorWithGoMod(cwd); root != "" {
			return root
		}
	}

	if exe, err := os.Executable(); err == nil {
		if root := findAncestorWithGoMod(filepath.Dir(exe)); root != "" {
			return root
		}
	}

	return ""
}

func findAncestorWithGoMod(start string) string {
	dir := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func Load() (*Config, error) {
	path := ConfigPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := Default()
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Save() error {
	path := ConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	dbPath := c.DBPath()
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}

func (c *Config) DBPath() string {
	return ExpandHome(c.Database.Path)
}

func ExpandHome(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
