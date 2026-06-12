package report

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/pricing"
)

type Filters struct {
	Since       string
	Until       string
	Channel     string
	Provider    string
	Model       string
	Session     string
	Project     string
	Timezone    string
	By          string
	SlowSort    string
	Limit       int
	CostMode    string
	PricingPath string
}

type ReportRow struct {
	Label                 string                   `json:"label"`
	Events                int64                    `json:"events"`
	TotalTokens           int64                    `json:"total_tokens"`
	InputTokens           int64                    `json:"input_tokens"`
	OutputTokens          int64                    `json:"output_tokens"`
	CacheCreationTokens   int64                    `json:"cache_creation_tokens"`
	CacheReadTokens       int64                    `json:"cache_read_tokens"`
	ReasoningTokens       int64                    `json:"reasoning_tokens"`
	AvgTotalDurationMs    *float64                 `json:"avg_total_duration_ms"`
	AvgTTFTMs             *float64                 `json:"avg_ttft_ms"`
	AvgOutputTPS          *float64                 `json:"avg_output_tps"`
	RecordedCostUSD       float64                  `json:"recorded_cost_usd"`
	EstimatedCostUSD      *float64                 `json:"estimated_cost_usd,omitempty"`
	EstimatedCostMicroUSD *int64                   `json:"estimated_cost_micro_usd,omitempty"`
	Pricing               *pricing.CoverageSummary `json:"pricing,omitempty"`
}

type SlowRow struct {
	EventID          string   `json:"event_id"`
	Timestamp        string   `json:"timestamp"`
	Channel          string   `json:"channel"`
	Model            string   `json:"model"`
	SessionID        string   `json:"session_id"`
	TotalTokens      int64    `json:"total_tokens"`
	OutputTokens     int64    `json:"output_tokens"`
	TotalDurationMs  *int64   `json:"total_duration_ms"`
	TTFTMs           *int64   `json:"ttft_ms"`
	OutputDurationMs *int64   `json:"output_duration_ms"`
	OutputTPS        *float64 `json:"output_tps"`
}

type TimeBreakdownRow struct {
	Bucket                string                   `json:"bucket"`
	Label                 string                   `json:"label"`
	Events                int64                    `json:"events"`
	TotalTokens           int64                    `json:"total_tokens"`
	InputTokens           int64                    `json:"input_tokens"`
	OutputTokens          int64                    `json:"output_tokens"`
	CacheCreationTokens   int64                    `json:"cache_creation_tokens"`
	CacheReadTokens       int64                    `json:"cache_read_tokens"`
	ReasoningTokens       int64                    `json:"reasoning_tokens"`
	AvgTotalDurationMs    *float64                 `json:"avg_total_duration_ms"`
	AvgTTFTMs             *float64                 `json:"avg_ttft_ms"`
	AvgOutputTPS          *float64                 `json:"avg_output_tps"`
	RecordedCostUSD       float64                  `json:"recorded_cost_usd"`
	EstimatedCostUSD      *float64                 `json:"estimated_cost_usd,omitempty"`
	EstimatedCostMicroUSD *int64                   `json:"estimated_cost_micro_usd,omitempty"`
	Pricing               *pricing.CoverageSummary `json:"pricing,omitempty"`
}

func Generate(conn *sql.DB, reportType string, filters Filters, asJSON bool) error {
	if err := validateDateFilters(filters); err != nil {
		return err
	}
	if err := validateCostMode(filters.CostMode); err != nil {
		return err
	}
	if reportType == "slow" && needsEstimatedCost(filters.CostMode) {
		return fmt.Errorf("estimated cost is not supported for slow reports yet")
	}
	switch reportType {
	case "daily":
		if filters.By != "" {
			return generateTimeBreakdown(conn, timeLabelExpr("date", "%Y-%m-%d", filters.Timezone), filters, asJSON)
		}
		return generateGrouped(conn, timeLabelExpr("date", "%Y-%m-%d", filters.Timezone), filters, asJSON, "Date", "label DESC")
	case "weekly":
		if filters.By != "" {
			return generateTimeBreakdown(conn, timeLabelExpr("strftime", "%Y-W%W", filters.Timezone), filters, asJSON)
		}
		return generateGrouped(conn, timeLabelExpr("strftime", "%Y-W%W", filters.Timezone), filters, asJSON, "Week", "label DESC")
	case "monthly":
		if filters.By != "" {
			return generateTimeBreakdown(conn, timeLabelExpr("strftime", "%Y-%m", filters.Timezone), filters, asJSON)
		}
		return generateGrouped(conn, timeLabelExpr("strftime", "%Y-%m", filters.Timezone), filters, asJSON, "Month", "label DESC")
	case "models":
		return generateGrouped(conn, "COALESCE(model_normalized, model_raw, 'unknown')", filters, asJSON, "Model", "total_tokens DESC")
	case "channels":
		return generateGrouped(conn, "COALESCE(channel, 'unknown')", filters, asJSON, "Channel", "total_tokens DESC")
	case "projects":
		return generateGrouped(conn, projectLabelExpr(), filters, asJSON, "Project", "total_tokens DESC LIMIT 100")
	case "sessions":
		return generateGrouped(conn, "COALESCE(session_path_id, session_id, 'no-session')", filters, asJSON, "Session", "total_tokens DESC LIMIT 50")
	case "slow":
		return generateSlow(conn, filters, asJSON)
	default:
		return fmt.Errorf("unknown report type: %s", reportType)
	}
}

func validateDateFilters(filters Filters) error {
	if filters.Since != "" {
		if err := validateDate("since", filters.Since); err != nil {
			return err
		}
	}
	if filters.Until != "" {
		if err := validateDate("until", filters.Until); err != nil {
			return err
		}
	}
	return nil
}

func validateCostMode(mode string) error {
	switch normalizedCostMode(mode) {
	case "recorded", "estimated", "both", "none":
		return nil
	default:
		return fmt.Errorf("invalid cost mode %q: expected recorded, estimated, both, or none", mode)
	}
}

func validateDate(name, value string) error {
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("%s must use YYYY-MM-DD", name)
	}
	return nil
}

func generateTimeBreakdown(conn *sql.DB, bucketExpr string, filters Filters, asJSON bool) error {
	labelExpr, labelHeader, err := reportBreakdownLabelExpr(filters.By)
	if err != nil {
		return err
	}
	estimates, err := estimateTimeBreakdownCosts(conn, bucketExpr, labelExpr, filters)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`SELECT
        %s AS bucket,
        %s AS label,
        COUNT(*) AS events,
        COALESCE(SUM(total_tokens), 0) AS total_tokens,
        COALESCE(SUM(`+effectiveInputTokensExpr()+`), 0) AS input_tokens,
        COALESCE(SUM(output_tokens), 0) AS output_tokens,
        COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
        COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
        COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
        AVG(total_duration_ms) AS avg_total_duration_ms,
        AVG(ttft_ms) AS avg_ttft_ms,
        AVG(output_tps) AS avg_output_tps,
        COALESCE(SUM(recorded_cost_usd), 0) AS recorded_cost_usd
    FROM usage_events WHERE 1=1`, bucketExpr, labelExpr)
	args := make([]any, 0)
	query = addFilters(query, &args, filters)
	query += " GROUP BY bucket, label ORDER BY bucket DESC, total_tokens DESC, label ASC"

	rows, err := conn.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	results := make([]TimeBreakdownRow, 0)
	for rows.Next() {
		var r TimeBreakdownRow
		var totalTokens, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens sql.NullInt64
		var avgDuration, avgTTFT, avgTPS, recordedCost sql.NullFloat64
		if err := rows.Scan(&r.Bucket, &r.Label, &r.Events, &totalTokens, &inputTokens, &outputTokens, &cacheCreationTokens, &cacheReadTokens, &reasoningTokens, &avgDuration, &avgTTFT, &avgTPS, &recordedCost); err != nil {
			return err
		}
		r.TotalTokens = nullInt64Value(totalTokens)
		r.InputTokens = nullInt64Value(inputTokens)
		r.OutputTokens = nullInt64Value(outputTokens)
		r.CacheCreationTokens = nullInt64Value(cacheCreationTokens)
		r.CacheReadTokens = nullInt64Value(cacheReadTokens)
		r.ReasoningTokens = nullInt64Value(reasoningTokens)
		r.AvgTotalDurationMs = nullFloat64(avgDuration)
		r.AvgTTFTMs = nullFloat64(avgTTFT)
		r.AvgOutputTPS = nullFloat64(avgTPS)
		if recordedCost.Valid {
			r.RecordedCostUSD = recordedCost.Float64
		}
		if estimate, ok := estimates[timeBreakdownEstimateKey(r.Bucket, r.Label)]; ok {
			attachTimeEstimate(&r, estimate)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if len(results) == 0 {
		fmt.Println("No data found for the specified filters.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	headers := append([]string{"Bucket", labelHeader, "Events", "Tokens", "Input", "Output", "Cache Create", "Cache Read", "Reasoning", "Avg TPS", "Avg TTFT(ms)"}, costHeaders(filters.CostMode)...)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(repeatStrings("---", len(headers)), "\t"))
	for _, r := range results {
		fields := []string{
			r.Bucket, truncate(r.Label, 40), fmt.Sprintf("%d", r.Events), fmt.Sprintf("%d", r.TotalTokens), fmt.Sprintf("%d", r.InputTokens), fmt.Sprintf("%d", r.OutputTokens),
			fmt.Sprintf("%d", r.CacheCreationTokens), fmt.Sprintf("%d", r.CacheReadTokens), fmt.Sprintf("%d", r.ReasoningTokens),
			formatFloatPtr(r.AvgOutputTPS), formatFloatPtr(r.AvgTTFTMs),
		}
		fmt.Fprintln(w, strings.Join(append(fields, costValues(filters.CostMode, r.RecordedCostUSD, r.EstimatedCostUSD, r.Pricing)...), "\t"))
	}
	return w.Flush()
}

func reportBreakdownLabelExpr(by string) (expr, header string, err error) {
	switch by {
	case "channel":
		return "COALESCE(channel, 'unknown')", "Channel", nil
	case "model":
		return "COALESCE(model_normalized, model_raw, 'unknown')", "Model", nil
	case "provider":
		return "COALESCE(provider, 'unknown')", "Provider", nil
	case "session":
		return "COALESCE(session_path_id, session_id, 'no-session')", "Session", nil
	case "project":
		return projectLabelExpr(), "Project", nil
	default:
		return "", "", fmt.Errorf("invalid time breakdown %q: expected channel, model, provider, session, or project", by)
	}
}

func generateGrouped(conn *sql.DB, labelExpr string, filters Filters, asJSON bool, labelHeader, order string) error {
	estimates, err := estimateGroupedCosts(conn, labelExpr, filters)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`SELECT
        %s AS label,
        COUNT(*) AS events,
        COALESCE(SUM(total_tokens), 0) AS total_tokens,
        COALESCE(SUM(`+effectiveInputTokensExpr()+`), 0) AS input_tokens,
        COALESCE(SUM(output_tokens), 0) AS output_tokens,
        COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens,
        COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
        COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
        AVG(total_duration_ms) AS avg_total_duration_ms,
        AVG(ttft_ms) AS avg_ttft_ms,
        AVG(output_tps) AS avg_output_tps,
        COALESCE(SUM(recorded_cost_usd), 0) AS recorded_cost_usd
    FROM usage_events WHERE 1=1`, labelExpr)
	args := make([]any, 0)
	query = addFilters(query, &args, filters)
	query += " GROUP BY label ORDER BY " + order
	return executeReport(conn, query, args, asJSON, labelHeader, filters, estimates)
}

func generateSlow(conn *sql.DB, filters Filters, asJSON bool) error {
	orderBy, err := slowOrderBy(filters.SlowSort)
	if err != nil {
		return err
	}
	limit := filters.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT
        event_id,
        timestamp_ms,
        channel,
        COALESCE(model_normalized, model_raw, 'unknown') AS model,
        COALESCE(session_path_id, session_id, '') AS session_id,
        total_tokens,
        output_tokens,
        total_duration_ms,
        ttft_ms,
        output_duration_ms,
        output_tps
    FROM usage_events WHERE (output_tps IS NOT NULL OR ttft_ms IS NOT NULL OR total_duration_ms IS NOT NULL)`
	args := make([]any, 0)
	query = addFilters(query, &args, filters)
	query += " ORDER BY " + orderBy + " LIMIT ?"
	args = append(args, limit)

	rows, err := conn.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	results := make([]SlowRow, 0)
	for rows.Next() {
		var item SlowRow
		var timestamp int64
		var totalDuration, ttft, outputDuration sql.NullInt64
		var outputTPS sql.NullFloat64
		if err := rows.Scan(&item.EventID, &timestamp, &item.Channel, &item.Model, &item.SessionID, &item.TotalTokens, &item.OutputTokens, &totalDuration, &ttft, &outputDuration, &outputTPS); err != nil {
			return err
		}
		item.Timestamp = time.UnixMilli(timestamp).UTC().Format(time.RFC3339)
		item.TotalDurationMs = nullInt64(totalDuration)
		item.TTFTMs = nullInt64(ttft)
		item.OutputDurationMs = nullInt64(outputDuration)
		item.OutputTPS = nullFloat64(outputTPS)
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if len(results) == 0 {
		fmt.Println("No slow/timing data found for the specified filters.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Time\tChannel\tModel\tSession\tOutput\tTPS\tTTFT(ms)\tTotal(ms)\n")
	fmt.Fprintf(w, "---\t---\t---\t---\t---\t---\t---\t---\n")
	for _, row := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			row.Timestamp, row.Channel, truncate(row.Model, 32), truncate(row.SessionID, 16), row.OutputTokens,
			formatFloatPtr(row.OutputTPS), formatIntPtr(row.TTFTMs), formatIntPtr(row.TotalDurationMs))
	}
	return w.Flush()
}

func effectiveInputTokensExpr() string {
	return `COALESCE(input_tokens, 0)`
}

func slowOrderBy(sortBy string) (string, error) {
	switch sortBy {
	case "", "output_tps":
		return "output_tps IS NULL ASC, output_tps ASC, ttft_ms DESC, total_duration_ms DESC", nil
	case "ttft_ms":
		return "ttft_ms IS NULL ASC, ttft_ms DESC, output_tps IS NULL ASC, output_tps ASC, total_duration_ms DESC", nil
	case "total_duration_ms":
		return "total_duration_ms IS NULL ASC, total_duration_ms DESC, output_tps IS NULL ASC, output_tps ASC, ttft_ms DESC", nil
	default:
		return "", fmt.Errorf("invalid slow sort %q: expected output_tps, ttft_ms, or total_duration_ms", sortBy)
	}
}

type reportCost struct {
	MicroUSD int64
	Summary  *pricing.CoverageSummary
}

func estimateGroupedCosts(conn *sql.DB, labelExpr string, filters Filters) (map[string]reportCost, error) {
	if !needsEstimatedCost(filters.CostMode) {
		return nil, nil
	}
	estimator, profile, err := reportEstimator(filters.PricingPath)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT
        %s AS label,
        timestamp_ms,
        channel,
        COALESCE(provider, ''),
        COALESCE(model_normalized, model_raw, 'unknown'),
        COALESCE(source_product, ''),
        COALESCE(observability_level, ''),
        COALESCE(token_accounting_method, ''),
        COALESCE(accounting_profile, ''),
        COALESCE(input_tokens, 0),
        COALESCE(output_tokens, 0),
        COALESCE(cache_creation_tokens, 0),
        COALESCE(cache_read_tokens, 0),
        COALESCE(reasoning_tokens, 0),
        COALESCE(total_tokens, 0)
    FROM usage_events WHERE 1=1`, labelExpr)
	args := make([]any, 0)
	query = addFilters(query, &args, filters)
	return scanEstimatedCosts(conn, query, args, estimator, profile, false)
}

func estimateTimeBreakdownCosts(conn *sql.DB, bucketExpr, labelExpr string, filters Filters) (map[string]reportCost, error) {
	if !needsEstimatedCost(filters.CostMode) {
		return nil, nil
	}
	estimator, profile, err := reportEstimator(filters.PricingPath)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT
        %s AS bucket,
        %s AS label,
        timestamp_ms,
        channel,
        COALESCE(provider, ''),
        COALESCE(model_normalized, model_raw, 'unknown'),
        COALESCE(source_product, ''),
        COALESCE(observability_level, ''),
        COALESCE(token_accounting_method, ''),
        COALESCE(accounting_profile, ''),
        COALESCE(input_tokens, 0),
        COALESCE(output_tokens, 0),
        COALESCE(cache_creation_tokens, 0),
        COALESCE(cache_read_tokens, 0),
        COALESCE(reasoning_tokens, 0),
        COALESCE(total_tokens, 0)
    FROM usage_events WHERE 1=1`, bucketExpr, labelExpr)
	args := make([]any, 0)
	query = addFilters(query, &args, filters)
	return scanEstimatedCosts(conn, query, args, estimator, profile, true)
}

func scanEstimatedCosts(conn *sql.DB, query string, args []any, estimator *pricing.Estimator, profile *pricing.Profile, hasBucket bool) (map[string]reportCost, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("pricing query failed: %w", err)
	}
	defer rows.Close()

	aggregates := make(map[string]*reportCostAccumulator)
	for rows.Next() {
		var bucket string
		var label string
		var ev pricing.Event
		if hasBucket {
			if err := rows.Scan(&bucket, &label, &ev.TimestampMs, &ev.Channel, &ev.Provider, &ev.Model, &ev.SourceProduct, &ev.ObservabilityLevel, &ev.TokenAccountingMethod, &ev.AccountingProfile, &ev.InputTokens, &ev.OutputTokens, &ev.CacheCreationTokens, &ev.CacheReadTokens, &ev.ReasoningTokens, &ev.TotalTokens); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&label, &ev.TimestampMs, &ev.Channel, &ev.Provider, &ev.Model, &ev.SourceProduct, &ev.ObservabilityLevel, &ev.TokenAccountingMethod, &ev.AccountingProfile, &ev.InputTokens, &ev.OutputTokens, &ev.CacheCreationTokens, &ev.CacheReadTokens, &ev.ReasoningTokens, &ev.TotalTokens); err != nil {
				return nil, err
			}
		}
		key := label
		if hasBucket {
			key = timeBreakdownEstimateKey(bucket, label)
		}
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = newReportCostAccumulator(estimator, profile)
			aggregates[key] = aggregate
		}
		if err := aggregate.Add(ev); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make(map[string]reportCost, len(aggregates))
	for key, aggregate := range aggregates {
		result, err := aggregate.Result()
		if err != nil {
			return nil, err
		}
		results[key] = result
	}
	return results, nil
}

type reportCostAccumulator struct {
	estimator *pricing.Estimator
	profile   *pricing.Profile
	coverage  pricing.Coverage
	buckets   map[string]*reportPricingBucket
}

type reportPricingBucket struct {
	match pricing.Match
	event pricing.Event
}

func newReportCostAccumulator(estimator *pricing.Estimator, profile *pricing.Profile) *reportCostAccumulator {
	return &reportCostAccumulator{
		estimator: estimator,
		profile:   profile,
		buckets:   make(map[string]*reportPricingBucket),
	}
}

func (a *reportCostAccumulator) Add(ev pricing.Event) error {
	match := a.estimator.Resolve(ev)
	if match.Rule == nil {
		a.coverage.Add(ev, pricing.Estimate{Confidence: "missing", MissingReason: match.MissingReason})
		return nil
	}
	a.coverage.Add(ev, pricing.Estimate{Priced: true, Confidence: match.Confidence})
	bucket := a.buckets[match.RuleID]
	if bucket == nil {
		copied := ev
		bucket = &reportPricingBucket{match: match, event: copied}
		a.buckets[match.RuleID] = bucket
		return nil
	}
	bucket.event.InputTokens += ev.InputTokens
	bucket.event.OutputTokens += ev.OutputTokens
	bucket.event.CacheCreationTokens += ev.CacheCreationTokens
	bucket.event.CacheReadTokens += ev.CacheReadTokens
	bucket.event.ReasoningTokens += ev.ReasoningTokens
	bucket.event.TotalTokens += ev.TotalTokens
	return nil
}

func (a *reportCostAccumulator) Result() (reportCost, error) {
	var micro int64
	for _, bucket := range a.buckets {
		estimate, err := a.estimator.EstimateMatch(bucket.event, bucket.match)
		if err != nil {
			return reportCost{}, err
		}
		micro += estimate.CostMicroUSD
	}
	return reportCost{MicroUSD: micro, Summary: a.coverage.Summary(a.profile)}, nil
}

func reportEstimator(path string) (*pricing.Estimator, *pricing.Profile, error) {
	var profile *pricing.Profile
	var err error
	if strings.TrimSpace(path) == "" {
		profile, err = pricing.LoadDefaultProfile()
	} else {
		profile, err = pricing.LoadProfileFile(path)
	}
	if err != nil {
		return nil, nil, err
	}
	estimator, err := pricing.NewEstimator(profile)
	if err != nil {
		return nil, nil, err
	}
	return estimator, profile, nil
}

func attachReportEstimate(row *ReportRow, estimate reportCost) {
	row.EstimatedCostMicroUSD = &estimate.MicroUSD
	usd := pricing.MicroUSDToUSD(estimate.MicroUSD)
	row.EstimatedCostUSD = &usd
	row.Pricing = estimate.Summary
}

func attachTimeEstimate(row *TimeBreakdownRow, estimate reportCost) {
	row.EstimatedCostMicroUSD = &estimate.MicroUSD
	usd := pricing.MicroUSDToUSD(estimate.MicroUSD)
	row.EstimatedCostUSD = &usd
	row.Pricing = estimate.Summary
}

func timeBreakdownEstimateKey(bucket, label string) string {
	return bucket + "\x00" + label
}

func normalizedCostMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "recorded"
	}
	return mode
}

func needsEstimatedCost(mode string) bool {
	switch normalizedCostMode(mode) {
	case "estimated", "both":
		return true
	default:
		return false
	}
}

func costHeaders(mode string) []string {
	switch normalizedCostMode(mode) {
	case "none":
		return nil
	case "estimated":
		return []string{"Estimated Cost(USD)", "Pricing Coverage", "Pricing Confidence"}
	case "both":
		return []string{"Recorded Cost(USD)", "Estimated Cost(USD)", "Pricing Coverage", "Pricing Confidence"}
	default:
		return []string{"Recorded Cost(USD)"}
	}
}

func costValues(mode string, recorded float64, estimated *float64, summary *pricing.CoverageSummary) []string {
	switch normalizedCostMode(mode) {
	case "none":
		return nil
	case "estimated":
		return []string{formatCostPtr(estimated), formatCoverage(summary), formatConfidence(summary)}
	case "both":
		return []string{fmt.Sprintf("$%.4f", recorded), formatCostPtr(estimated), formatCoverage(summary), formatConfidence(summary)}
	default:
		return []string{fmt.Sprintf("$%.4f", recorded)}
	}
}

func repeatStrings(value string, count int) []string {
	items := make([]string, count)
	for i := range items {
		items[i] = value
	}
	return items
}

func formatCostPtr(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("$%.4f", *value)
}

func formatCoverage(summary *pricing.CoverageSummary) string {
	if summary == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", summary.CoverageRatio*100)
}

func formatConfidence(summary *pricing.CoverageSummary) string {
	if summary == nil || summary.Confidence == "" {
		return "-"
	}
	return summary.Confidence
}

func executeReport(conn *sql.DB, query string, args []any, asJSON bool, labelHeader string, filters Filters, estimates map[string]reportCost) error {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	results := make([]ReportRow, 0)
	for rows.Next() {
		var r ReportRow
		var totalTokens, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens sql.NullInt64
		var avgDuration, avgTTFT, avgTPS, recordedCost sql.NullFloat64
		if err := rows.Scan(&r.Label, &r.Events, &totalTokens, &inputTokens, &outputTokens, &cacheCreationTokens, &cacheReadTokens, &reasoningTokens, &avgDuration, &avgTTFT, &avgTPS, &recordedCost); err != nil {
			return err
		}
		r.TotalTokens = nullInt64Value(totalTokens)
		r.InputTokens = nullInt64Value(inputTokens)
		r.OutputTokens = nullInt64Value(outputTokens)
		r.CacheCreationTokens = nullInt64Value(cacheCreationTokens)
		r.CacheReadTokens = nullInt64Value(cacheReadTokens)
		r.ReasoningTokens = nullInt64Value(reasoningTokens)
		r.AvgTotalDurationMs = nullFloat64(avgDuration)
		r.AvgTTFTMs = nullFloat64(avgTTFT)
		r.AvgOutputTPS = nullFloat64(avgTPS)
		if recordedCost.Valid {
			r.RecordedCostUSD = recordedCost.Float64
		}
		if estimate, ok := estimates[r.Label]; ok {
			attachReportEstimate(&r, estimate)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if len(results) == 0 {
		fmt.Println("No data found for the specified filters.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	headers := append([]string{labelHeader, "Events", "Tokens", "Input", "Output", "Cache Create", "Cache Read", "Reasoning", "Avg TPS", "Avg TTFT(ms)"}, costHeaders(filters.CostMode)...)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	fmt.Fprintln(w, strings.Join(repeatStrings("---", len(headers)), "\t"))
	for _, r := range results {
		fields := []string{
			truncate(r.Label, 40), fmt.Sprintf("%d", r.Events), fmt.Sprintf("%d", r.TotalTokens), fmt.Sprintf("%d", r.InputTokens), fmt.Sprintf("%d", r.OutputTokens),
			fmt.Sprintf("%d", r.CacheCreationTokens), fmt.Sprintf("%d", r.CacheReadTokens), fmt.Sprintf("%d", r.ReasoningTokens),
			formatFloatPtr(r.AvgOutputTPS), formatFloatPtr(r.AvgTTFTMs),
		}
		fmt.Fprintln(w, strings.Join(append(fields, costValues(filters.CostMode, r.RecordedCostUSD, r.EstimatedCostUSD, r.Pricing)...), "\t"))
	}
	_ = w.Flush()

	var totalEvents int64
	var totalTokens int64
	for _, r := range results {
		totalEvents += r.Events
		totalTokens += r.TotalTokens
	}
	fmt.Printf("\nTotal: %d events, %d tokens\n", totalEvents, totalTokens)
	return nil
}

func addFilters(query string, args *[]any, filters Filters) string {
	if filters.Since != "" {
		query += " AND timestamp_ms >= ?"
		*args = append(*args, dateStartMillis(filters.Since, filters.Timezone))
	}
	if filters.Until != "" {
		query += " AND timestamp_ms < ?"
		*args = append(*args, dateAfterMillis(filters.Until, filters.Timezone))
	}
	if filters.Channel != "" {
		query += " AND channel = ?"
		*args = append(*args, filters.Channel)
	}
	if filters.Provider != "" {
		query += " AND provider = ?"
		*args = append(*args, filters.Provider)
	}
	if filters.Model != "" {
		query += " AND (model_normalized = ? OR model_raw = ?)"
		*args = append(*args, filters.Model, filters.Model)
	}
	if filters.Session != "" {
		query += " AND (session_id = ? OR session_path_id = ?)"
		*args = append(*args, filters.Session, filters.Session)
	}
	if filters.Project != "" {
		query += " AND (project_path = ? OR " + projectLabelExpr() + " = ?)"
		*args = append(*args, filters.Project, filters.Project)
	}
	return query
}

func projectLabelExpr() string {
	return "agentledger_project_label(project_path)"
}

func dateStartMillis(value, timezone string) int64 {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	loc := reportTimezoneLocation(timezone)
	localDate := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	return localDate.UTC().UnixMilli()
}

func dateAfterMillis(value, timezone string) int64 {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	loc := reportTimezoneLocation(timezone)
	localDate := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	return localDate.AddDate(0, 0, 1).UTC().UnixMilli()
}

func timeLabelExpr(fn, format, timezone string) string {
	modifier := sqliteTimezoneModifier(timezone)
	if fn == "date" {
		if modifier == "" {
			return "date(timestamp_ms/1000, 'unixepoch')"
		}
		return fmt.Sprintf("date(timestamp_ms/1000, 'unixepoch', '%s')", modifier)
	}
	if modifier == "" {
		return fmt.Sprintf("strftime('%s', timestamp_ms/1000, 'unixepoch')", format)
	}
	return fmt.Sprintf("strftime('%s', timestamp_ms/1000, 'unixepoch', '%s')", format, modifier)
}

func sqliteTimezoneModifier(timezone string) string {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || strings.EqualFold(timezone, "UTC") {
		return ""
	}
	if strings.EqualFold(timezone, "Local") {
		return "localtime"
	}
	if modifier, ok := fixedOffsetModifier(timezone); ok {
		return modifier
	}
	loc := reportTimezoneLocation(timezone)
	if loc == time.UTC {
		return ""
	}
	_, offset := time.Now().In(loc).Zone()
	return offsetModifier(offset)
}

func reportTimezoneLocation(timezone string) *time.Location {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || strings.EqualFold(timezone, "UTC") {
		return time.UTC
	}
	if strings.EqualFold(timezone, "Local") {
		return time.Local
	}
	if modifier, ok := fixedOffsetModifier(timezone); ok {
		seconds := modifierSeconds(modifier)
		return time.FixedZone(timezone, seconds)
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func fixedOffsetModifier(value string) (string, bool) {
	if len(value) != 6 || (value[0] != '+' && value[0] != '-') || value[3] != ':' {
		return "", false
	}
	if value[1] < '0' || value[1] > '9' || value[2] < '0' || value[2] > '9' || value[4] < '0' || value[4] > '9' || value[5] < '0' || value[5] > '9' {
		return "", false
	}
	return value, true
}

func modifierSeconds(modifier string) int {
	sign := 1
	if modifier[0] == '-' {
		sign = -1
	}
	hours := int(modifier[1]-'0')*10 + int(modifier[2]-'0')
	minutes := int(modifier[4]-'0')*10 + int(modifier[5]-'0')
	return sign * ((hours * 3600) + (minutes * 60))
}

func offsetModifier(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}

func nullInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullInt64Value(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func formatIntPtr(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func formatFloatPtr(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *value)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func FormatTimestamp(ms int64) string {
	if ms == 0 {
		return "N/A"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}
