package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show database statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, database, err := openReadOnlyV2ConfiguredDatabase()
		if err != nil {
			return err
		}
		defer database.Close()

		stats, err := database.GetStats()
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}

		totalCost, _ := stats["total_recorded_cost_usd"].(float64)
		totalEvents, _ := stats["total_events"].(int64)
		knownRequests, _ := stats["known_request_count"].(int64)
		knownRequestEvents, _ := stats["request_count_known_events"].(int64)
		unknownRequestEvents, _ := stats["request_count_unknown_events"].(int64)
		requestValue := "—"
		if knownRequestEvents > 0 {
			requestValue = fmt.Sprintf("%d", knownRequests)
			if unknownRequestEvents > 0 {
				requestValue += "+"
			}
		}

		fmt.Println("AgentLedger Status")
		fmt.Println("==================")
		fmt.Printf("Database: %s\n", cfg.DBPath())
		fmt.Printf("Schema version:    %v\n", stats["schema_version"])
		fmt.Printf("Total events:      %v\n", stats["total_events"])
		fmt.Printf("Known requests:    %s\n", requestValue)
		fmt.Printf("Request coverage:  %d/%d events\n", knownRequestEvents, totalEvents)
		fmt.Printf("Import runs:       %v\n", stats["total_import_runs"])
		fmt.Printf("Total tokens:      %v\n", stats["total_tokens"])
		fmt.Printf("Recorded cost USD: $%.4f\n", totalCost)
		return nil
	},
}
