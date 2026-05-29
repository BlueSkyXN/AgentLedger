package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export database to portable .aldb file",
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		if output == "" {
			output = "agent-ledger-export.aldb"
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		bytesWritten, err := exportDatabase(cfg.DBPath(), output, cfg.Privacy.RedactPathsOnExport)
		if err != nil {
			return err
		}

		if cfg.Privacy.RedactPathsOnExport {
			fmt.Printf("✓ Exported redacted database (%d bytes) to %s\n", bytesWritten, output)
			return nil
		}
		fmt.Printf("✓ Exported unredacted database (%d bytes) to %s\n", bytesWritten, output)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringP("output", "o", "", "Output file path")
}

func exportDatabase(sourcePath, outputPath string, redact bool) (int64, error) {
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return 0, fmt.Errorf("invalid output path: %w", err)
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("invalid source database path: %w", err)
	}
	sourceInfo, err := os.Stat(absSource)
	if err != nil {
		return 0, fmt.Errorf("cannot access source database: %w", err)
	}
	if sourceInfo.IsDir() {
		return 0, fmt.Errorf("source database path is a directory")
	}
	if absOutput == absSource {
		return 0, fmt.Errorf("output path must be different from the source database path")
	}
	if outputInfo, err := os.Stat(absOutput); err == nil && os.SameFile(sourceInfo, outputInfo) {
		return 0, fmt.Errorf("output path must be different from the source database path")
	} else if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("cannot access output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absOutput), 0o755); err != nil {
		return 0, fmt.Errorf("failed to create output directory: %w", err)
	}

	sourceConn, err := openExportSource(absSource)
	if err != nil {
		return 0, err
	}
	defer sourceConn.Close()

	tempFile, err := os.CreateTemp(filepath.Dir(absOutput), ".agent-ledger-export-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("failed to create temporary export file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return 0, fmt.Errorf("failed to close temporary export file: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return 0, fmt.Errorf("failed to prepare temporary export path: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if _, err := sourceConn.Exec(fmt.Sprintf("VACUUM main INTO '%s'", sqliteString(tempPath))); err != nil {
		return 0, fmt.Errorf("failed to export database: %w", err)
	}
	if redact {
		if err := redactExportedDatabase(tempPath); err != nil {
			return 0, err
		}
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat output file: %w", err)
	}
	bytesWritten := info.Size()
	if err := os.Rename(tempPath, absOutput); err != nil {
		return 0, fmt.Errorf("failed to replace output file: %w", err)
	}
	return bytesWritten, nil
}

func openExportSource(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", path))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	var version string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to validate source database schema: %w", err)
	}
	if version != db.SchemaVersion {
		_ = conn.Close()
		return nil, fmt.Errorf("incompatible database schema version %s; expected %s", version, db.SchemaVersion)
	}
	return conn, nil
}

func redactExportedDatabase(path string) error {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return fmt.Errorf("failed to open exported database for redaction: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(`PRAGMA secure_delete = ON`); err != nil {
		return fmt.Errorf("failed to enable secure delete for exported database: %w", err)
	}
	if _, err := conn.Exec(`
		UPDATE usage_events SET
			project_path = NULL,
			source_file = NULL,
			raw_usage_json = NULL
	`); err != nil {
		return fmt.Errorf("failed to redact exported database: %w", err)
	}
	if _, err := conn.Exec(`UPDATE import_runs SET error = NULL WHERE error IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to redact import run warnings: %w", err)
	}
	if _, err := conn.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("failed to compact redacted exported database: %w", err)
	}
	return nil
}

func sqliteString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
