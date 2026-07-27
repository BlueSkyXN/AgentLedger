package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadOnlyUsesDefaultsWithoutCreatingFiles(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-dir")
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	cfg, err := LoadReadOnly()
	if err != nil {
		t.Fatalf("load read-only: %v", err)
	}
	if cfg.DBPath() != filepath.Join(dataDir, "agent-ledger.db") {
		t.Fatalf("database path = %q", cfg.DBPath())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("read-only load created data directory: %v", err)
	}
}

func TestLoadReadOnlyReadsExistingConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	cfg := Default()
	cfg.Reports.Timezone = "UTC"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := LoadReadOnly()
	if err != nil {
		t.Fatalf("load read-only: %v", err)
	}
	if loaded.Reports.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC", loaded.Reports.Timezone)
	}
}

func TestLoadReadOnlyBackfillsWorkBuddyDefaults(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

	legacy := strings.Join([]string{
		"[database]",
		`path = "agent-ledger.db"`,
		"",
		"[agents.claude]",
		"enabled = false",
	}, "\n")
	if err := os.WriteFile(ConfigPath(), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	cfg, err := LoadReadOnly()
	if err != nil {
		t.Fatalf("load legacy config: %v", err)
	}
	if !cfg.Agents.WorkBuddy.Enabled {
		t.Fatal("expected missing WorkBuddy config to retain enabled default")
	}
	if len(cfg.Agents.WorkBuddy.Paths) != 1 || cfg.Agents.WorkBuddy.Paths[0] != "~/.workbuddy/projects" {
		t.Fatalf("unexpected WorkBuddy defaults: %+v", cfg.Agents.WorkBuddy)
	}
	if cfg.Agents.Claude.Enabled {
		t.Fatal("expected explicit legacy Claude setting to remain disabled")
	}
}

func TestValidateUsageEvidenceWritePolicy(t *testing.T) {
	if got := Default().Privacy.Mode; got != PrivacyModeStatistics {
		t.Fatalf("default privacy mode = %q, want %q", got, PrivacyModeStatistics)
	}

	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "statistics", mode: PrivacyModeStatistics},
		{name: "legacy envelope alias", mode: PrivacyModeEnvelope},
		{name: "full", mode: "full", wantErr: true},
		{name: "none", mode: "none", wantErr: true},
		{name: "empty", mode: "", wantErr: true},
		{name: "case variant", mode: "Envelope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Privacy.Mode = tt.mode
			err := cfg.ValidateUsageEvidenceWritePolicy()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateUsageEvidenceWritePolicy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
