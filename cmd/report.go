package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/report"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report [type]",
	Short: "Generate usage reports",
	Long:  "Available report types: daily, weekly, monthly, models, channels, devices, sessions",
}

var reportDailyCmd = &cobra.Command{
	Use:   "daily",
	Short: "Daily usage breakdown",
	RunE:  runReport("daily"),
}

var reportWeeklyCmd = &cobra.Command{
	Use:   "weekly",
	Short: "Weekly usage summary",
	RunE:  runReport("weekly"),
}

var reportMonthlyCmd = &cobra.Command{
	Use:   "monthly",
	Short: "Monthly usage summary",
	RunE:  runReport("monthly"),
}

var reportModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Model breakdown",
	RunE:  runReport("models"),
}

var reportChannelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "Channel breakdown",
	RunE:  runReport("channels"),
}

var reportDevicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "Device breakdown",
	RunE:  runReport("devices"),
}

var reportSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Session listing",
	RunE:  runReport("sessions"),
}

func init() {
	reportCmd.AddCommand(reportDailyCmd)
	reportCmd.AddCommand(reportWeeklyCmd)
	reportCmd.AddCommand(reportMonthlyCmd)
	reportCmd.AddCommand(reportModelsCmd)
	reportCmd.AddCommand(reportChannelsCmd)
	reportCmd.AddCommand(reportDevicesCmd)
	reportCmd.AddCommand(reportSessionsCmd)

	for _, cmd := range []*cobra.Command{reportDailyCmd, reportWeeklyCmd, reportMonthlyCmd, reportModelsCmd, reportChannelsCmd, reportDevicesCmd, reportSessionsCmd} {
		cmd.Flags().String("since", "", "Start date (YYYY-MM-DD)")
		cmd.Flags().String("until", "", "End date (YYYY-MM-DD)")
		cmd.Flags().String("by", "", "Group by (agent, model, provider)")
		cmd.Flags().Bool("json", false, "Output as JSON")
	}
}

func runReport(reportType string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		since, _ := cmd.Flags().GetString("since")
		until, _ := cmd.Flags().GetString("until")
		groupBy, _ := cmd.Flags().GetString("by")
		asJSON, _ := cmd.Flags().GetBool("json")

		return report.Generate(database.Conn(), reportType, since, until, groupBy, asJSON)
	}
}
