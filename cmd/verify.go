package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify database integrity",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return err
		}
		defer database.Close()

		var result string
		err = database.Conn().QueryRow("PRAGMA integrity_check").Scan(&result)
		if err != nil {
			return fmt.Errorf("integrity check failed: %w", err)
		}

		if result == "ok" {
			fmt.Println("✓ Database integrity check passed")
		} else {
			fmt.Printf("✗ Integrity issues: %s\n", result)
		}
		return nil
	},
}
