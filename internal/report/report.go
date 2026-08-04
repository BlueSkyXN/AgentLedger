package report

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/BlueSkyXN/AgentLedger/internal/analytics"
	"github.com/BlueSkyXN/AgentLedger/internal/pricing"
)

type Filters struct {
	Since           string
	Until           string
	Channel         string
	SourceProduct   string
	Provider        string
	Model           string
	Session         string
	Project         string
	Timezone        string
	CostMode        string
	PricingPath     string
	PricingExplicit bool
}

func Generate(conn *sql.DB, reportType string, filters Filters, asJSON bool) error {
	costMode := strings.ToLower(strings.TrimSpace(filters.CostMode))
	if costMode == "" {
		costMode = "estimated"
	}
	if costMode != "estimated" && costMode != "none" {
		return fmt.Errorf("unsupported --cost %q: expected estimated or none", filters.CostMode)
	}
	if filters.PricingExplicit {
		if strings.TrimSpace(filters.PricingPath) == "" {
			return fmt.Errorf("explicit --pricing path cannot be empty")
		}
		if _, err := pricing.LoadProfileFile(filters.PricingPath); err != nil {
			return fmt.Errorf("invalid --pricing profile: %w", err)
		}
	}

	analyticsFilters := analytics.Filters{
		Since: filters.Since, Until: filters.Until,
		Channel: filters.Channel, SourceProduct: filters.SourceProduct,
		Provider: filters.Provider, Model: filters.Model,
		Session: filters.Session, Project: filters.Project,
		Timezone: filters.Timezone, CostMode: costMode, PricingPath: filters.PricingPath,
	}

	switch reportType {
	case "daily", "weekly", "monthly":
		rows, err := analytics.BuildTimeseries(conn, reportType, analyticsFilters)
		if err != nil {
			return err
		}
		return outputMetrics(rows, "Date", costMode, asJSON)
	case "models", "channels", "sources", "providers", "projects":
		dimension := map[string]string{
			"models": "model", "channels": "channel", "sources": "source_product",
			"providers": "provider", "projects": "project",
		}[reportType]
		rows, err := analytics.BuildBreakdown(conn, dimension, analyticsFilters)
		if err != nil {
			return err
		}
		return outputMetrics(rows, strings.Title(strings.TrimSuffix(reportType, "s")), costMode, asJSON)
	case "sessions":
		result, err := analytics.BuildSessions(conn, analyticsFilters, 1_000_000, 0)
		if err != nil {
			return err
		}
		return outputSessions(result.Items, costMode, asJSON)
	default:
		return fmt.Errorf("unsupported report type %q", reportType)
	}
}

func outputMetrics(rows []analytics.MetricRow, labelHeader, costMode string, asJSON bool) error {
	if asJSON {
		return writeJSON(rows)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if costMode == "estimated" {
		fmt.Fprintf(w, "%s\tEvents\tInput\tOutput\tReasoning\tCache create\tCache read\tTotal\tEstimated USD\tPricing\n", labelHeader)
	} else {
		fmt.Fprintf(w, "%s\tEvents\tInput\tOutput\tReasoning\tCache create\tCache read\tTotal\n", labelHeader)
	}
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d", row.Label, row.Events, row.InputTokens, row.OutputTokens, row.ReasoningTokens, row.CacheCreationTokens, row.CacheReadTokens, row.TotalTokens)
		if costMode == "estimated" {
			fmt.Fprintf(w, "\t%s\t%s", formatCost(row.EstimatedCostUSD), formatPricing(row.Pricing))
		}
		fmt.Fprintln(w)
	}
	return w.Flush()
}

func outputSessions(rows []analytics.Session, costMode string, asJSON bool) error {
	if asJSON {
		return writeJSON(rows)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if costMode == "estimated" {
		fmt.Fprintln(w, "Session\tFirst\tLast\tChannel\tSource\tPrimary model\tModels\tEvents\tInput\tOutput\tReasoning\tCache create\tCache read\tTotal\tEstimated USD\tPricing")
	} else {
		fmt.Fprintln(w, "Session\tFirst\tLast\tChannel\tSource\tPrimary model\tModels\tEvents\tInput\tOutput\tReasoning\tCache create\tCache read\tTotal")
	}
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d",
			row.SessionKey, row.FirstDate, row.LastDate, row.Channel, row.SourceProduct, row.PrimaryModel,
			row.ModelCount, row.EventCount, row.InputTokens, row.OutputTokens, row.ReasoningTokens,
			row.CacheCreationTokens, row.CacheReadTokens, row.TotalTokens)
		if costMode == "estimated" {
			fmt.Fprintf(w, "\t%s\t%s", formatCost(row.EstimatedCostUSD), formatPricing(row.Pricing))
		}
		fmt.Fprintln(w)
	}
	return w.Flush()
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func formatCost(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.6f", *value)
}

func formatPricing(info *analytics.PricingInfo) string {
	if info == nil {
		return "none"
	}
	if info.Status != "available" {
		return info.Status + ":" + info.ErrorCode
	}
	return fmt.Sprintf("priced=%d unpriced=%d policy_zero=%d", info.PricedEvents, info.UnpricedEvents, info.PolicyZeroEvents)
}
