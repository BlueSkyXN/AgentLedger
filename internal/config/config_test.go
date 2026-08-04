package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultContainsOnlyV3ConfigurationSurface(t *testing.T) {
	t.Setenv("AGENT_LEDGER_DATA_DIR", t.TempDir())
	cfg := Default()
	if cfg.Import.GracingMinutes != 15 || cfg.Reports.Timezone == "" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !cfg.Privacy.RedactPathsOnExport {
		t.Fatal("redacted export must default to true")
	}
	if cfg.Reports.PricingPath != "" {
		t.Fatalf("default pricing path should use embedded profile, got %q", cfg.Reports.PricingPath)
	}
	if cfg.DBPath() != filepath.Join(DataDir(), "agent-ledger.db") {
		t.Fatalf("unexpected DB path %q", cfg.DBPath())
	}
}

func TestSavedConfigOmitsRemovedV2Keys(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := Default()
	cfg.Reports.PricingPath = "~/pricing.json"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, removed := range []string{"[cleanup]", "single_thread", "currency", "mode =", "envelope"} {
		if strings.Contains(text, removed) {
			t.Errorf("saved v3 config contains removed key %q:\n%s", removed, text)
		}
	}
	for _, required := range []string{"redact_paths_on_export", "gracing_minutes", "timezone", "pricing_path"} {
		if !strings.Contains(text, required) {
			t.Errorf("saved v3 config missing %q", required)
		}
	}
}

func TestLoadReadOnlyDoesNotCreateConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	if _, err := LoadReadOnly(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("LoadReadOnly created config: %v", err)
	}
}
