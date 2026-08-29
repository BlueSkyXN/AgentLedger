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
	if !cfg.Agents.Cursor.Enabled || len(cfg.Agents.Cursor.Paths) == 0 {
		t.Fatalf("Cursor adapter should be enabled with discovery roots: %#v", cfg.Agents.Cursor)
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
	for _, removed := range []string{"[cleanup]", "single_thread", "currency", "mode =", "envelope", "[agents.trae-work-cn]", "experimental"} {
		if strings.Contains(text, removed) {
			t.Errorf("saved v3 config contains removed key %q:\n%s", removed, text)
		}
	}
	for _, required := range []string{"redact_paths_on_export", "gracing_minutes", "timezone", "pricing_path", "[agents.cursor]", "[agents.workbuddy]"} {
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

func TestLoadIgnoresRemovedTraeWorkCNSection(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := Default()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n[agents.trae-work-cn]\nenabled = true\nexperimental = true\npaths = [\"/unused.app\"]\n")...)
	if err := os.WriteFile(ConfigPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Agents.WorkBuddy.Enabled {
		t.Fatalf("existing adapters should still load after a leftover TRAE section: %#v", loaded.Agents)
	}
}

func TestLoadIgnoresRemovedExperimentalFlag(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)
	cfg := Default()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\nexperimental = true\n")...)
	if err := os.WriteFile(ConfigPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Agents.WorkBuddy.Enabled || !loaded.Agents.Codex.Enabled {
		t.Fatalf("legacy experimental key should be ignored: %#v", loaded.Agents)
	}
}
