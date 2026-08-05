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

		cfg, err := config.LoadReadOnly()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		result, err := mergeDatabase(cfg.DBPath(), incomingPath)
		if err != nil {
			if result.Rejected > 0 {
				fmt.Printf("Merge preflight rejected %d event(s):\n", result.Rejected)
				for _, conflict := range result.Conflicts {
					fmt.Printf("  %s: %d\n", conflict.Code, conflict.Count)
				}
			}
			return fmt.Errorf("merge failed: %w", err)
		}

		fmt.Printf("Merge complete:\n")
		fmt.Printf("  Events inserted: %d\n", result.Added)
		fmt.Printf("  Events updated:  %d\n", result.Updated)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", result.Skipped)
		return nil
	},
}

func mergeDatabase(destinationPath, incomingPath string) (db.MergeResult, error) {
	database, err := db.OpenReadWriteV3(destinationPath)
	if err != nil {
		return db.MergeResult{}, fmt.Errorf("failed to open destination database: %w", err)
	}
	defer database.Close()
	return database.MergeFrom(incomingPath)
}
