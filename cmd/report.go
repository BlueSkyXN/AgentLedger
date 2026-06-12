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
	Long:  "Available report types: daily, weekly, monthly, models, channels, projects, sessions, slow",
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

var reportProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Project breakdown",
	RunE:  runReport("projects"),
}

var reportSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Session listing",
	RunE:  runReport("sessions"),
}

var reportSlowCmd = &cobra.Command{
	Use:   "slow",
	Short: "Slow and timing event listing",
	RunE:  runReport("slow"),
}

func init() {
	reportCmd.AddCommand(reportDailyCmd)
	reportCmd.AddCommand(reportWeeklyCmd)
	reportCmd.AddCommand(reportMonthlyCmd)
	reportCmd.AddCommand(reportModelsCmd)
	reportCmd.AddCommand(reportChannelsCmd)
	reportCmd.AddCommand(reportProjectsCmd)
	reportCmd.AddCommand(reportSessionsCmd)
	reportCmd.AddCommand(reportSlowCmd)

	for _, cmd := range []*cobra.Command{reportDailyCmd, reportWeeklyCmd, reportMonthlyCmd, reportModelsCmd, reportChannelsCmd, reportProjectsCmd, reportSessionsCmd, reportSlowCmd} {
		cmd.Flags().String("since", "", "Start date (YYYY-MM-DD)")
		cmd.Flags().String("until", "", "End date (YYYY-MM-DD)")
		cmd.Flags().String("channel", "", "Filter by agent source channel")
		cmd.Flags().String("provider", "", "Filter by provider")
		cmd.Flags().String("model", "", "Filter by normalized or raw model name")
		cmd.Flags().String("session", "", "Filter by session ID")
		cmd.Flags().String("project", "", "Filter by project label or raw project path")
		cmd.Flags().String("cost", "recorded", "Cost mode: recorded, estimated, both, or none")
		cmd.Flags().String("pricing", "", "Path to pricing JSON profile for estimated cost")
		cmd.Flags().Bool("json", false, "Output as JSON")
	}
	for _, cmd := range []*cobra.Command{reportDailyCmd, reportWeeklyCmd, reportMonthlyCmd} {
		cmd.Flags().String("by", "", "Break down time buckets by channel, model, provider, session, or project")
	}
	reportSlowCmd.Flags().String("sort", "output_tps", "Sort slow report by output_tps, ttft_ms, or total_duration_ms")
	reportSlowCmd.Flags().Int("limit", 50, "Maximum slow events to return")
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
		channel, _ := cmd.Flags().GetString("channel")
		provider, _ := cmd.Flags().GetString("provider")
		modelName, _ := cmd.Flags().GetString("model")
		session, _ := cmd.Flags().GetString("session")
		project, _ := cmd.Flags().GetString("project")
		costMode, _ := cmd.Flags().GetString("cost")
		pricingPath, _ := cmd.Flags().GetString("pricing")
		asJSON, _ := cmd.Flags().GetBool("json")
		by := ""
		if cmd.Flags().Lookup("by") != nil {
			by, _ = cmd.Flags().GetString("by")
		}
		sortBy := "output_tps"
		limit := 50
		if cmd.Flags().Lookup("sort") != nil {
			sortBy, _ = cmd.Flags().GetString("sort")
		}
		if cmd.Flags().Lookup("limit") != nil {
			limit, _ = cmd.Flags().GetInt("limit")
		}

		return report.Generate(database.Conn(), reportType, report.Filters{
			Since:       since,
			Until:       until,
			Channel:     channel,
			Provider:    provider,
			Model:       modelName,
			Session:     session,
			Project:     project,
			Timezone:    cfg.Reports.Timezone,
			By:          by,
			SlowSort:    sortBy,
			Limit:       limit,
			CostMode:    costMode,
			PricingPath: pricingPath,
		}, asJSON)
	}
}
