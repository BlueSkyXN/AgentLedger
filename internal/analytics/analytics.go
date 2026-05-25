package analytics

import (
	"database/sql"
	"fmt"
	"time"
)

type Filters struct {
	Since    string
	Until    string
	Channel  string
	Provider string
	Model    string
	Session  string
}

type Summary struct {
	TotalEvents         int64    `json:"total_events"`
	ImportRuns          int64    `json:"import_runs"`
	TotalTokens         int64    `json:"total_tokens"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	RecordedCostUSD     float64  `json:"recorded_cost_usd"`
	AvgTotalDurationMs  *float64 `json:"avg_total_duration_ms"`
	AvgTTFTMs           *float64 `json:"avg_ttft_ms"`
	AvgOutputTPS        *float64 `json:"avg_output_tps"`
	FirstEventAt        *string  `json:"first_event_at"`
	LastEventAt         *string  `json:"last_event_at"`
}

type MetricRow struct {
	Label               string   `json:"label"`
	Events              int64    `json:"events"`
	TotalTokens         int64    `json:"total_tokens"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	RecordedCostUSD     float64  `json:"recorded_cost_usd"`
	AvgTotalDurationMs  *float64 `json:"avg_total_duration_ms"`
	AvgTTFTMs           *float64 `json:"avg_ttft_ms"`
	AvgOutputTPS        *float64 `json:"avg_output_tps"`
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
	query := `SELECT
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(input_tokens), 0),
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
	return &s, nil
}

func BuildTimeseries(conn *sql.DB, bucket string, filters Filters) ([]MetricRow, error) {
	var labelExpr string
	switch bucket {
	case "daily", "":
		labelExpr = "date(timestamp_ms/1000, 'unixepoch')"
	case "weekly":
		labelExpr = "strftime('%Y-W%W', timestamp_ms/1000, 'unixepoch')"
	case "monthly":
		labelExpr = "strftime('%Y-%m', timestamp_ms/1000, 'unixepoch')"
	default:
		return nil, fmt.Errorf("unsupported bucket %q", bucket)
	}
	query := groupedMetricQuery(labelExpr)
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY label ASC"
	return scanMetricRows(conn, query, args...)
}

func BuildBreakdown(conn *sql.DB, by string, filters Filters) ([]MetricRow, error) {
	var labelExpr string
	switch by {
	case "channel", "":
		labelExpr = "COALESCE(channel, 'unknown')"
	case "model":
		labelExpr = "COALESCE(model_normalized, model_raw, 'unknown')"
	case "provider":
		labelExpr = "COALESCE(provider, 'unknown')"
	case "session":
		labelExpr = "COALESCE(session_id, 'no-session')"
	default:
		return nil, fmt.Errorf("unsupported breakdown %q", by)
	}
	query := groupedMetricQuery(labelExpr)
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT 100"
	return scanMetricRows(conn, query, args...)
}

func BuildSessions(conn *sql.DB, filters Filters, limit int) ([]MetricRow, error) {
	query := groupedMetricQuery("COALESCE(session_id, 'no-session')")
	var args []any
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT ?"
	args = append(args, limit)
	return scanMetricRows(conn, query, args...)
}

func BuildSlow(conn *sql.DB, sortBy string, filters Filters, limit int) ([]Event, error) {
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
	models, err := distinctStrings(conn, "COALESCE(model_normalized, model_raw)")
	if err != nil {
		return nil, err
	}
	sessions, err := distinctStrings(conn, "session_id")
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
		COALESCE(SUM(input_tokens), 0),
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
		*args = append(*args, dateStartMillis(filters.Since))
	}
	if filters.Until != "" {
		query += fmt.Sprintf(" AND %s < ?", timestampExpr)
		*args = append(*args, dateAfterMillis(filters.Until))
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
		query += " AND session_id = ?"
		*args = append(*args, filters.Session)
	}
	return query
}

func eventSelect() string {
	return `SELECT event_id, dedupe_strategy, channel, provider, model_raw, model_normalized,
		timestamp_ms, session_id, project_path, message_id, request_id,
		COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		COALESCE(cache_creation_tokens, 0), COALESCE(cache_read_tokens, 0), COALESCE(reasoning_tokens, 0),
		COALESCE(total_tokens, 0), total_duration_ms, ttft_ms, output_duration_ms, output_tps, recorded_cost_usd`
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
		var provider, modelRaw, modelNormalized, sessionID, projectPath, messageID, requestID sql.NullString
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

func distinctStrings(conn *sql.DB, expr string) ([]string, error) {
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

func dateStartMillis(value string) int64 {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	return parsed.UTC().UnixMilli()
}

func dateAfterMillis(value string) int64 {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0
	}
	return parsed.AddDate(0, 0, 1).UTC().UnixMilli()
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
