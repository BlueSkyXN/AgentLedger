package analytics

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/pricing"
)

type Filters struct {
	Since    string
	Until    string
	Channel  string
	Provider string
	Model    string
	Session  string
	Timezone string
}

type Summary struct {
	TotalEvents           int64                    `json:"total_events"`
	ImportRuns            int64                    `json:"import_runs"`
	TotalTokens           int64                    `json:"total_tokens"`
	InputTokens           int64                    `json:"input_tokens"`
	OutputTokens          int64                    `json:"output_tokens"`
	CacheCreationTokens   int64                    `json:"cache_creation_tokens"`
	CacheReadTokens       int64                    `json:"cache_read_tokens"`
	ReasoningTokens       int64                    `json:"reasoning_tokens"`
	RecordedCostUSD       float64                  `json:"recorded_cost_usd"`
	AvgTotalDurationMs    *float64                 `json:"avg_total_duration_ms"`
	AvgTTFTMs             *float64                 `json:"avg_ttft_ms"`
	AvgOutputTPS          *float64                 `json:"avg_output_tps"`
	FirstEventAt          *string                  `json:"first_event_at"`
	LastEventAt           *string                  `json:"last_event_at"`
	EstimatedCostUSD      *float64                 `json:"estimated_cost_usd,omitempty"`
	EstimatedCostMicroUSD *int64                   `json:"estimated_cost_micro_usd,omitempty"`
	Pricing               *pricing.CoverageSummary `json:"pricing,omitempty"`
}

type MetricRow struct {
	Label                 string                   `json:"label"`
	Events                int64                    `json:"events"`
	TotalTokens           int64                    `json:"total_tokens"`
	InputTokens           int64                    `json:"input_tokens"`
	OutputTokens          int64                    `json:"output_tokens"`
	CacheCreationTokens   int64                    `json:"cache_creation_tokens"`
	CacheReadTokens       int64                    `json:"cache_read_tokens"`
	ReasoningTokens       int64                    `json:"reasoning_tokens"`
	RecordedCostUSD       float64                  `json:"recorded_cost_usd"`
	AvgTotalDurationMs    *float64                 `json:"avg_total_duration_ms"`
	AvgTTFTMs             *float64                 `json:"avg_ttft_ms"`
	AvgOutputTPS          *float64                 `json:"avg_output_tps"`
	EstimatedCostUSD      *float64                 `json:"estimated_cost_usd,omitempty"`
	EstimatedCostMicroUSD *int64                   `json:"estimated_cost_micro_usd,omitempty"`
	Pricing               *pricing.CoverageSummary `json:"pricing,omitempty"`
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
	RecordedCostUSD       float64                  `json:"recorded_cost_usd"`
	AvgTotalDurationMs    *float64                 `json:"avg_total_duration_ms"`
	AvgTTFTMs             *float64                 `json:"avg_ttft_ms"`
	AvgOutputTPS          *float64                 `json:"avg_output_tps"`
	EstimatedCostUSD      *float64                 `json:"estimated_cost_usd,omitempty"`
	EstimatedCostMicroUSD *int64                   `json:"estimated_cost_micro_usd,omitempty"`
	Pricing               *pricing.CoverageSummary `json:"pricing,omitempty"`
}

type ImportRun struct {
	ID            string  `json:"id"`
	StartedAt     *string `json:"started_at"`
	FinishedAt    *string `json:"finished_at"`
	Status        string  `json:"status"`
	FilesScanned  int64   `json:"files_scanned"`
	EventsAdded   int64   `json:"events_added"`
	EventsUpdated int64   `json:"events_updated"`
	EventsSkipped int64   `json:"events_skipped"`
	Error         *string `json:"error"`
}

type Event struct {
	EventID             string   `json:"event_id"`
	DedupeStrategy      string   `json:"dedupe_strategy"`
	Channel             string   `json:"channel"`
	Provider            *string  `json:"provider"`
	ModelRaw            *string  `json:"model_raw"`
	ModelNormalized     *string  `json:"model_normalized"`
	Timestamp           *string  `json:"timestamp"`
	SessionID           *string  `json:"session_id"`
	SessionPathID       *string  `json:"session_path_id"`
	TurnID              *string  `json:"turn_id"`
	ProjectPath         *string  `json:"project_path"`
	MessageID           *string  `json:"message_id"`
	RequestID           *string  `json:"request_id"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	TotalTokens         int64    `json:"total_tokens"`
	TotalDurationMs     *int64   `json:"total_duration_ms"`
	TTFTMs              *int64   `json:"ttft_ms"`
	OutputDurationMs    *int64   `json:"output_duration_ms"`
	OutputTPS           *float64 `json:"output_tps"`
	RecordedCostUSD     *float64 `json:"recorded_cost_usd"`
}

type FilterOptions struct {
	Channels  []string `json:"channels"`
	Providers []string `json:"providers"`
	Models    []string `json:"models"`
	Sessions  []string `json:"sessions"`
}

func BuildSummary(conn *sql.DB, filters Filters) (*Summary, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	query := `SELECT
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(` + effectiveInputTokensExpr() + `), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(recorded_cost_usd), 0),
		AVG(total_duration_ms),
		AVG(ttft_ms),
		AVG(output_tps),
		MIN(timestamp_ms),
		MAX(timestamp_ms)
	FROM usage_events WHERE 1=1`
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")

	var s Summary
	var avgDuration, avgTTFT, avgTPS sql.NullFloat64
	var firstMs, lastMs sql.NullInt64
	if err := conn.QueryRow(query, args...).Scan(
		&s.TotalEvents,
		&s.TotalTokens,
		&s.InputTokens,
		&s.OutputTokens,
		&s.CacheCreationTokens,
		&s.CacheReadTokens,
		&s.ReasoningTokens,
		&s.RecordedCostUSD,
		&avgDuration,
		&avgTTFT,
		&avgTPS,
		&firstMs,
		&lastMs,
	); err != nil {
		return nil, err
	}
	s.AvgTotalDurationMs = nullableFloat(avgDuration)
	s.AvgTTFTMs = nullableFloat(avgTTFT)
	s.AvgOutputTPS = nullableFloat(avgTPS)
	if firstMs.Valid {
		s.FirstEventAt = stringPtr(formatMillis(firstMs.Int64))
	}
	if lastMs.Valid {
		s.LastEventAt = stringPtr(formatMillis(lastMs.Int64))
	}
	_ = conn.QueryRow("SELECT COUNT(*) FROM import_runs").Scan(&s.ImportRuns)
	costs, err := estimateGroupedCosts(conn, "'summary'", filters)
	if err != nil {
		return nil, err
	}
	if estimate, ok := costs["summary"]; ok {
		attachSummaryEstimate(&s, estimate)
	}
	return &s, nil
}

func BuildTimeseries(conn *sql.DB, bucket string, filters Filters) ([]MetricRow, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	labelExpr, err := timeBucketExpr(bucket, filters.Timezone)
	if err != nil {
		return nil, err
	}
	query := groupedMetricQuery(labelExpr)
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY label ASC"
	rows, err := scanMetricRows(conn, query, args...)
	if err != nil {
		return nil, err
	}
	return attachMetricCosts(conn, rows, labelExpr, filters)
}

func BuildTimeseriesBreakdown(conn *sql.DB, bucket, by string, filters Filters) ([]TimeBreakdownRow, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	bucketExpr, err := timeBucketExpr(bucket, filters.Timezone)
	if err != nil {
		return nil, err
	}
	labelExpr, err := breakdownLabelExpr(by)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT
		%s AS bucket,
		%s AS label,
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(`+effectiveInputTokensExpr()+`), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(recorded_cost_usd), 0),
		AVG(total_duration_ms),
		AVG(ttft_ms),
		AVG(output_tps)
	FROM usage_events WHERE 1=1`, bucketExpr, labelExpr)
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY bucket, label ORDER BY bucket ASC, COALESCE(SUM(total_tokens), 0) DESC, label ASC"
	rows, err := scanTimeBreakdownRows(conn, query, args...)
	if err != nil {
		return nil, err
	}
	return attachTimeBreakdownCosts(conn, rows, bucketExpr, labelExpr, filters)
}

func timeBucketExpr(bucket, timezone string) (string, error) {
	switch bucket {
	case "daily", "":
		return timeLabelExpr("date", "%Y-%m-%d", timezone), nil
	case "weekly":
		return timeLabelExpr("strftime", "%Y-W%W", timezone), nil
	case "monthly":
		return timeLabelExpr("strftime", "%Y-%m", timezone), nil
	default:
		return "", fmt.Errorf("unsupported bucket %q", bucket)
	}
}

func BuildBreakdown(conn *sql.DB, by string, filters Filters) ([]MetricRow, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	labelExpr, err := breakdownLabelExpr(by)
	if err != nil {
		return nil, err
	}
	query := groupedMetricQuery(labelExpr)
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT 100"
	rows, err := scanMetricRows(conn, query, args...)
	if err != nil {
		return nil, err
	}
	return attachMetricCosts(conn, rows, labelExpr, filters)
}

func breakdownLabelExpr(by string) (string, error) {
	switch by {
	case "channel", "":
		return "COALESCE(channel, 'unknown')", nil
	case "model":
		return "COALESCE(model_normalized, model_raw, 'unknown')", nil
	case "provider":
		return "COALESCE(provider, 'unknown')", nil
	case "session":
		return "COALESCE(session_path_id, session_id, 'no-session')", nil
	default:
		return "", fmt.Errorf("unsupported breakdown %q", by)
	}
}

func BuildSessions(conn *sql.DB, filters Filters, limit int) ([]MetricRow, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	query := groupedMetricQuery("COALESCE(session_path_id, session_id, 'no-session')")
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT ?"
	args = append(args, limit)
	rows, err := scanMetricRows(conn, query, args...)
	if err != nil {
		return nil, err
	}
	return attachMetricCosts(conn, rows, "COALESCE(session_path_id, session_id, 'no-session')", filters)
}

func BuildSlow(conn *sql.DB, sortBy string, filters Filters, limit int) ([]Event, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	orderExpr, notNullExpr, err := slowOrder(sortBy)
	if err != nil {
		return nil, err
	}
	query := eventSelect() + " FROM usage_events WHERE " + notNullExpr + " IS NOT NULL"
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " ORDER BY " + orderExpr + " LIMIT ?"
	args = append(args, limit)
	return scanEvents(conn, query, args...)
}

func ListImportRuns(conn *sql.DB, limit int) ([]ImportRun, error) {
	rows, err := conn.Query(`SELECT id, started_at_ms, finished_at_ms, status,
		COALESCE(files_scanned, 0), COALESCE(events_added, 0), COALESCE(events_updated, 0), COALESCE(events_skipped, 0), error
		FROM import_runs ORDER BY started_at_ms DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ImportRun
	for rows.Next() {
		var item ImportRun
		var started, finished sql.NullInt64
		var errText sql.NullString
		if err := rows.Scan(&item.ID, &started, &finished, &item.Status, &item.FilesScanned, &item.EventsAdded, &item.EventsUpdated, &item.EventsSkipped, &errText); err != nil {
			return nil, err
		}
		if started.Valid {
			item.StartedAt = stringPtr(formatMillis(started.Int64))
		}
		if finished.Valid {
			item.FinishedAt = stringPtr(formatMillis(finished.Int64))
		}
		item.Error = nullableString(errText)
		results = append(results, item)
	}
	return results, rows.Err()
}

func ListEvents(conn *sql.DB, filters Filters, limit int) ([]Event, error) {
	if err := validateDateFilters(filters); err != nil {
		return nil, err
	}
	query := eventSelect() + " FROM usage_events WHERE 1=1"
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " ORDER BY timestamp_ms DESC LIMIT ?"
	args = append(args, limit)
	return scanEvents(conn, query, args...)
}

func BuildFilterOptions(conn *sql.DB) (*FilterOptions, error) {
	channels, err := distinctStrings(conn, "channel")
	if err != nil {
		return nil, err
	}
	providers, err := distinctStrings(conn, "provider")
	if err != nil {
		return nil, err
	}
	models, err := distinctStrings(conn, "model")
	if err != nil {
		return nil, err
	}
	sessions, err := distinctStrings(conn, "session")
	if err != nil {
		return nil, err
	}
	return &FilterOptions{Channels: channels, Providers: providers, Models: models, Sessions: sessions}, nil
}

func groupedMetricQuery(labelExpr string) string {
	return fmt.Sprintf(`SELECT
		%s AS label,
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(`+effectiveInputTokensExpr()+`), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(recorded_cost_usd), 0),
		AVG(total_duration_ms),
		AVG(ttft_ms),
		AVG(output_tps)
	FROM usage_events WHERE 1=1`, labelExpr)
}

func addFilters(query string, args *[]any, filters Filters, timestampExpr string) string {
	if filters.Since != "" {
		query += fmt.Sprintf(" AND %s >= ?", timestampExpr)
		*args = append(*args, dateStartMillis(filters.Since, filters.Timezone))
	}
	if filters.Until != "" {
		query += fmt.Sprintf(" AND %s < ?", timestampExpr)
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
	return query
}

func eventSelect() string {
	return `SELECT event_id, dedupe_strategy, channel, provider, model_raw, model_normalized,
		timestamp_ms, session_id, session_path_id, turn_id, project_path, message_id, request_id,
		` + effectiveInputTokensExpr() + `, COALESCE(output_tokens, 0),
		COALESCE(cache_creation_tokens, 0), COALESCE(cache_read_tokens, 0), COALESCE(reasoning_tokens, 0),
		COALESCE(total_tokens, 0), total_duration_ms, ttft_ms, output_duration_ms, output_tps, recorded_cost_usd`
}

func effectiveInputTokensExpr() string {
	return `COALESCE(input_tokens, 0)`
}

func scanEvents(conn *sql.DB, query string, args ...any) ([]Event, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Event
	for rows.Next() {
		var item Event
		var provider, modelRaw, modelNormalized, sessionID, sessionPathID, turnID, projectPath, messageID, requestID sql.NullString
		var ts sql.NullInt64
		var totalDuration, ttft, outputDuration sql.NullInt64
		var outputTPS, recordedCost sql.NullFloat64
		if err := rows.Scan(
			&item.EventID,
			&item.DedupeStrategy,
			&item.Channel,
			&provider,
			&modelRaw,
			&modelNormalized,
			&ts,
			&sessionID,
			&sessionPathID,
			&turnID,
			&projectPath,
			&messageID,
			&requestID,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CacheCreationTokens,
			&item.CacheReadTokens,
			&item.ReasoningTokens,
			&item.TotalTokens,
			&totalDuration,
			&ttft,
			&outputDuration,
			&outputTPS,
			&recordedCost,
		); err != nil {
			return nil, err
		}
		item.Provider = nullableString(provider)
		item.ModelRaw = nullableString(modelRaw)
		item.ModelNormalized = nullableString(modelNormalized)
		item.SessionID = nullableString(sessionID)
		item.SessionPathID = nullableString(sessionPathID)
		item.TurnID = nullableString(turnID)
		item.ProjectPath = nullableString(projectPath)
		item.MessageID = nullableString(messageID)
		item.RequestID = nullableString(requestID)
		item.TotalDurationMs = nullableInt(totalDuration)
		item.TTFTMs = nullableInt(ttft)
		item.OutputDurationMs = nullableInt(outputDuration)
		item.OutputTPS = nullableFloat(outputTPS)
		item.RecordedCostUSD = nullableFloat(recordedCost)
		if ts.Valid {
			item.Timestamp = stringPtr(formatMillis(ts.Int64))
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func scanMetricRows(conn *sql.DB, query string, args ...any) ([]MetricRow, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MetricRow
	for rows.Next() {
		var item MetricRow
		var avgDuration, avgTTFT, avgTPS sql.NullFloat64
		if err := rows.Scan(&item.Label, &item.Events, &item.TotalTokens, &item.InputTokens, &item.OutputTokens, &item.CacheCreationTokens, &item.CacheReadTokens, &item.ReasoningTokens, &item.RecordedCostUSD, &avgDuration, &avgTTFT, &avgTPS); err != nil {
			return nil, err
		}
		item.AvgTotalDurationMs = nullableFloat(avgDuration)
		item.AvgTTFTMs = nullableFloat(avgTTFT)
		item.AvgOutputTPS = nullableFloat(avgTPS)
		results = append(results, item)
	}
	return results, rows.Err()
}

func scanTimeBreakdownRows(conn *sql.DB, query string, args ...any) ([]TimeBreakdownRow, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TimeBreakdownRow
	for rows.Next() {
		var item TimeBreakdownRow
		var avgDuration, avgTTFT, avgTPS sql.NullFloat64
		if err := rows.Scan(&item.Bucket, &item.Label, &item.Events, &item.TotalTokens, &item.InputTokens, &item.OutputTokens, &item.CacheCreationTokens, &item.CacheReadTokens, &item.ReasoningTokens, &item.RecordedCostUSD, &avgDuration, &avgTTFT, &avgTPS); err != nil {
			return nil, err
		}
		item.AvgTotalDurationMs = nullableFloat(avgDuration)
		item.AvgTTFTMs = nullableFloat(avgTTFT)
		item.AvgOutputTPS = nullableFloat(avgTPS)
		results = append(results, item)
	}
	return results, rows.Err()
}

type costResult struct {
	MicroUSD int64
	Summary  *pricing.CoverageSummary
}

type costAccumulator struct {
	estimator *pricing.Estimator
	profile   *pricing.Profile
	coverage  pricing.Coverage
	buckets   map[string]*pricingBucket
}

type pricingBucket struct {
	match pricing.Match
	event pricing.Event
}

func attachSummaryEstimate(row *Summary, estimate costResult) {
	row.EstimatedCostMicroUSD = &estimate.MicroUSD
	usd := pricing.MicroUSDToUSD(estimate.MicroUSD)
	row.EstimatedCostUSD = &usd
	row.Pricing = estimate.Summary
}

func attachMetricEstimate(row *MetricRow, estimate costResult) {
	row.EstimatedCostMicroUSD = &estimate.MicroUSD
	usd := pricing.MicroUSDToUSD(estimate.MicroUSD)
	row.EstimatedCostUSD = &usd
	row.Pricing = estimate.Summary
}

func attachTimeEstimate(row *TimeBreakdownRow, estimate costResult) {
	row.EstimatedCostMicroUSD = &estimate.MicroUSD
	usd := pricing.MicroUSDToUSD(estimate.MicroUSD)
	row.EstimatedCostUSD = &usd
	row.Pricing = estimate.Summary
}

func attachMetricCosts(conn *sql.DB, rows []MetricRow, labelExpr string, filters Filters) ([]MetricRow, error) {
	costs, err := estimateGroupedCosts(conn, labelExpr, filters)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if estimate, ok := costs[rows[i].Label]; ok {
			attachMetricEstimate(&rows[i], estimate)
		}
	}
	return rows, nil
}

func attachTimeBreakdownCosts(conn *sql.DB, rows []TimeBreakdownRow, bucketExpr, labelExpr string, filters Filters) ([]TimeBreakdownRow, error) {
	costs, err := estimateTimeBreakdownCosts(conn, bucketExpr, labelExpr, filters)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if estimate, ok := costs[timeBreakdownEstimateKey(rows[i].Bucket, rows[i].Label)]; ok {
			attachTimeEstimate(&rows[i], estimate)
		}
	}
	return rows, nil
}

func estimateGroupedCosts(conn *sql.DB, labelExpr string, filters Filters) (map[string]costResult, error) {
	estimator, profile, err := analyticsEstimator()
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
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	return scanEstimatedCosts(conn, query, args, estimator, profile, false)
}

func estimateTimeBreakdownCosts(conn *sql.DB, bucketExpr, labelExpr string, filters Filters) (map[string]costResult, error) {
	estimator, profile, err := analyticsEstimator()
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
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	return scanEstimatedCosts(conn, query, args, estimator, profile, true)
}

func scanEstimatedCosts(conn *sql.DB, query string, args []any, estimator *pricing.Estimator, profile *pricing.Profile, hasBucket bool) (map[string]costResult, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("pricing query failed: %w", err)
	}
	defer rows.Close()

	aggregates := make(map[string]*costAccumulator)
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
			aggregate = newCostAccumulator(estimator, profile)
			aggregates[key] = aggregate
		}
		if err := aggregate.Add(ev); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make(map[string]costResult, len(aggregates))
	for key, aggregate := range aggregates {
		result, err := aggregate.Result()
		if err != nil {
			return nil, err
		}
		results[key] = result
	}
	return results, nil
}

func newCostAccumulator(estimator *pricing.Estimator, profile *pricing.Profile) *costAccumulator {
	return &costAccumulator{estimator: estimator, profile: profile, buckets: make(map[string]*pricingBucket)}
}

func (a *costAccumulator) Add(ev pricing.Event) error {
	match := a.estimator.Resolve(ev)
	if match.Rule == nil {
		a.coverage.Add(ev, pricing.Estimate{Confidence: "missing", MissingReason: match.MissingReason})
		return nil
	}
	a.coverage.Add(ev, pricing.Estimate{Priced: true, Confidence: match.Confidence})
	bucket := a.buckets[match.RuleID]
	if bucket == nil {
		copied := ev
		a.buckets[match.RuleID] = &pricingBucket{match: match, event: copied}
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

func (a *costAccumulator) Result() (costResult, error) {
	var micro int64
	for _, bucket := range a.buckets {
		estimate, err := a.estimator.EstimateMatch(bucket.event, bucket.match)
		if err != nil {
			return costResult{}, err
		}
		micro += estimate.CostMicroUSD
	}
	return costResult{MicroUSD: micro, Summary: a.coverage.Summary(a.profile)}, nil
}

func analyticsEstimator() (*pricing.Estimator, *pricing.Profile, error) {
	profile, err := pricing.LoadDefaultProfile()
	if err != nil {
		return nil, nil, err
	}
	estimator, err := pricing.NewEstimator(profile)
	if err != nil {
		return nil, nil, err
	}
	return estimator, profile, nil
}

func timeBreakdownEstimateKey(bucket, label string) string {
	return bucket + "\x00" + label
}

func slowOrder(sortBy string) (orderExpr, notNullExpr string, err error) {
	switch sortBy {
	case "output_tps", "":
		return "output_tps ASC", "output_tps", nil
	case "ttft_ms":
		return "ttft_ms DESC", "ttft_ms", nil
	case "total_duration_ms":
		return "total_duration_ms DESC", "total_duration_ms", nil
	default:
		return "", "", fmt.Errorf("unsupported slow sort %q", sortBy)
	}
}

func distinctStrings(conn *sql.DB, key string) ([]string, error) {
	expr, err := filterOptionExpr(key)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(fmt.Sprintf(`SELECT DISTINCT %s AS value FROM usage_events WHERE %s IS NOT NULL AND %s != '' ORDER BY value ASC LIMIT 500`, expr, expr, expr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func filterOptionExpr(key string) (string, error) {
	switch key {
	case "channel":
		return "channel", nil
	case "provider":
		return "provider", nil
	case "model":
		return "COALESCE(model_normalized, model_raw)", nil
	case "session":
		return "COALESCE(session_path_id, session_id)", nil
	default:
		return "", fmt.Errorf("unsupported filter option %q", key)
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

func validateDate(name, value string) error {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return nil
	}
	if _, ok := parseDateTime(value, ""); ok {
		return nil
	}
	return fmt.Errorf("%s must use YYYY-MM-DD or RFC3339 datetime", name)
}

func dateStartMillis(value, timezone string) int64 {
	if millis, ok := parseDateTime(value, timezone); ok {
		return millis
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	loc := reportTimezoneLocation(timezone)
	localDate := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	return localDate.UTC().UnixMilli()
}

func dateAfterMillis(value, timezone string) int64 {
	if millis, ok := parseDateTime(value, timezone); ok {
		return millis
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	loc := reportTimezoneLocation(timezone)
	localDate := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	return localDate.AddDate(0, 0, 1).UTC().UnixMilli()
}

func parseDateTime(value, timezone string) (int64, bool) {
	if !strings.Contains(value, "T") {
		return 0, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().UnixMilli(), true
	}
	loc := reportTimezoneLocation(timezone)
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed.UTC().UnixMilli(), true
		}
	}
	return 0, false
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
		return time.FixedZone(timezone, modifierSeconds(modifier))
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

func formatMillis(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func stringPtr(value string) *string {
	return &value
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}
