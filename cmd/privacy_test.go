package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
)

func TestWriteCommandsRejectUnsupportedPrivacyModeBeforeOpeningDatabase(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "import", run: func() error { return importCmd.RunE(importCmd, nil) }},
		{name: "merge", run: func() error { return mergeCmd.RunE(mergeCmd, []string{"incoming.aldb"}) }},
		{name: "compact raw dry run", run: func() error { return executeCompactRaw(true, false, io.Discard) }},
		{name: "compact raw apply", run: func() error { return executeCompactRaw(false, true, io.Discard) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			t.Setenv("AGENT_LEDGER_DATA_DIR", dataDir)

			cfg := config.Default()
			cfg.Privacy.Mode = "full"
			if err := cfg.Save(); err != nil {
				t.Fatalf("save config: %v", err)
			}

			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "unsupported privacy.mode") {
				t.Fatalf("expected privacy mode error, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "agent-ledger.db")); !os.IsNotExist(err) {
				t.Fatalf("write command opened or created database before rejecting privacy mode: %v", err)
			}
		})
	}
}
