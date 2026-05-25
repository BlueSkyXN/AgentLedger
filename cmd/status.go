package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show database statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		stats, err := database.GetStats()
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}

		totalCost, _ := stats["total_cost_usd"].(float64)

		fmt.Println("AgentLedger Status")
		fmt.Println("==================")
		fmt.Printf("Database: %s\n", cfg.DBPath())
		fmt.Printf("Total events:      %v\n", stats["total_events"])
		fmt.Printf("Total devices:     %v\n", stats["total_devices"])
		fmt.Printf("Import runs:       %v\n", stats["total_import_runs"])
		fmt.Printf("Source files:      %v\n", stats["total_source_files"])
		fmt.Printf("Total tokens:      %v\n", stats["total_tokens"])
		fmt.Printf("Total cost (USD):  $%.4f\n", totalCost)
		return nil
	},
}
