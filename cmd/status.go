package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show database statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, database, err := openReadOnlyV3ConfiguredDatabase()
		if err != nil {
			return err
		}
		defer database.Close()

		stats, err := database.GetStats()
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}

		fmt.Println("AgentLedger Status")
		fmt.Println("==================")
		fmt.Printf("Database: %s\n", cfg.DBPath())
		fmt.Printf("Schema version:    %v\n", stats["schema_version"])
		fmt.Printf("Identity version:  %v\n", stats["identity_version"])
		fmt.Printf("Total events:      %v\n", stats["total_events"])
		fmt.Printf("Total sessions:    %v\n", stats["total_sessions"])
		fmt.Printf("Import runs:       %v\n", stats["total_import_runs"])
		fmt.Printf("Total tokens:      %v\n", stats["total_tokens"])
		return nil
	},
}
