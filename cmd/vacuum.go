package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var vacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Vacuum the database to reclaim space",
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

		_, err = database.Conn().Exec("VACUUM")
		if err != nil {
			return fmt.Errorf("vacuum failed: %w", err)
		}

		fmt.Println("✓ Database vacuumed successfully")
		return nil
	},
}
