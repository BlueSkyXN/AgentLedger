package cmd

import (
	"fmt"
	"os"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var initReset bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize database and config",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if initReset {
			for _, path := range []string{cfg.DBPath(), cfg.DBPath() + "-wal", cfg.DBPath() + "-shm"} {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed to remove %s: %w", path, err)
				}
			}
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		fmt.Println("✓ Config created at:", config.ConfigPath())
		fmt.Println("✓ Database created at:", cfg.DBPath())
		fmt.Println("✓ Schema version:", db.SchemaVersion)
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initReset, "reset", false, "Delete and recreate the local AgentLedger database")
}
