package cmd

import (
	"github.com/BlueSkyXN/AgentLedger/internal/report"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report [type]",
	Short: "Generate usage reports",
	Long:  "Available report types: daily, weekly, monthly, models, channels, sources, providers, projects, sessions",
}

func reportCommand(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, RunE: runReport(use)}
}

var (
	reportDailyCmd     = reportCommand("daily", "Daily usage breakdown")
	reportWeeklyCmd    = reportCommand("weekly", "Weekly usage summary")
	reportMonthlyCmd   = reportCommand("monthly", "Monthly usage summary")
	reportModelsCmd    = reportCommand("models", "Model breakdown")
	reportChannelsCmd  = reportCommand("channels", "Channel breakdown")
	reportSourcesCmd   = reportCommand("sources", "Source product breakdown")
	reportProvidersCmd = reportCommand("providers", "Provider breakdown")
	reportProjectsCmd  = reportCommand("projects", "Project breakdown")
	reportSessionsCmd  = reportCommand("sessions", "Session listing")
)

func init() {
	commands := []*cobra.Command{
		reportDailyCmd, reportWeeklyCmd, reportMonthlyCmd, reportModelsCmd,
		reportChannelsCmd, reportSourcesCmd, reportProvidersCmd, reportProjectsCmd, reportSessionsCmd,
	}
	for _, command := range commands {
		reportCmd.AddCommand(command)
		command.Flags().String("since", "", "Start date (YYYY-MM-DD or RFC3339)")
		command.Flags().String("until", "", "End date (YYYY-MM-DD or RFC3339)")
		command.Flags().String("channel", "", "Filter by channel")
		command.Flags().String("source", "", "Filter by source product")
		command.Flags().String("provider", "", "Filter by provider")
		command.Flags().String("model", "", "Filter by normalized model ID")
		command.Flags().String("session", "", "Filter by session key or native session ID")
		command.Flags().String("project", "", "Filter by project label")
		command.Flags().String("cost", "estimated", "Cost mode: estimated or none")
		command.Flags().String("pricing", "", "Override pricing JSON profile")
		command.Flags().Bool("json", false, "Output as JSON")
	}
}

func runReport(reportType string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, database, err := openReadOnlyV3ConfiguredDatabase()
		if err != nil {
			return err
		}
		defer database.Close()

		pricingPath, _ := cmd.Flags().GetString("pricing")
		pricingExplicit := cmd.Flags().Changed("pricing")
		if !pricingExplicit {
			pricingPath = cfg.Reports.PricingPath
		}
		filters := report.Filters{
			Timezone:        cfg.Reports.Timezone,
			PricingPath:     pricingPath,
			PricingExplicit: pricingExplicit,
		}
		filters.Since, _ = cmd.Flags().GetString("since")
		filters.Until, _ = cmd.Flags().GetString("until")
		filters.Channel, _ = cmd.Flags().GetString("channel")
		filters.SourceProduct, _ = cmd.Flags().GetString("source")
		filters.Provider, _ = cmd.Flags().GetString("provider")
		filters.Model, _ = cmd.Flags().GetString("model")
		filters.Session, _ = cmd.Flags().GetString("session")
		filters.Project, _ = cmd.Flags().GetString("project")
		filters.CostMode, _ = cmd.Flags().GetString("cost")
		asJSON, _ := cmd.Flags().GetBool("json")
		return report.Generate(database.Conn(), reportType, filters, asJSON)
	}
}
