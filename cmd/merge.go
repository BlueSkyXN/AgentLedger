package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var mergeCmd = &cobra.Command{
	Use:   "merge [file.aldb]",
	Short: "Merge another .aldb database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		incomingPath := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		inserted, skipped, err := database.MergeFrom(incomingPath)
		if err != nil {
			return fmt.Errorf("merge failed: %w", err)
		}

		fmt.Printf("Merge complete:\n")
		fmt.Printf("  Events inserted: %d\n", inserted)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", skipped)
		return nil
	},
}
