package analytics

import (
	"database/sql"
	"fmt"
	"time"
)

type Filters struct {
	Since string
	Until string
}

type Summary struct {
	TotalEvents         int64   `json:"total_events"`
	TotalDevices        int64   `json:"total_devices"`
	ImportRuns          int64   `json:"import_runs"`
	TotalTokens         int64   `json:"total_tokens"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	TotalCostUSD        float64 `json:"total_cost_usd"`
	FirstEventAt        *string `json:"first_event_at"`
	LastEventAt         *string `json:"last_event_at"`
}

type MetricRow struct {
	Label               string  `json:"label"`
	Events              int64   `json:"events"`
	TotalTokens         int64   `json:"total_tokens"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

type ImportRun struct {
	ID            string  `json:"id"`
	DeviceID      string  `json:"device_id"`
	StartedAt     *string `json:"started_at"`
	FinishedAt    *string `json:"finished_at"`
	Status        string  `json:"status"`
	FilesScanned  int64   `json:"files_scanned"`
	EventsAdded   int64   `json:"events_added"`
	EventsSkipped int64   `json:"events_skipped"`
	Error         *string `json:"error"`
}

type Event struct {
	EventFingerprint    string  `json:"event_fingerprint"`
	FingerprintStrategy string  `json:"fingerprint_strategy"`
	Agent               string  `json:"agent"`
	Provider            *string `json:"provider"`
	ModelRaw            *string `json:"model_raw"`
	ModelNormalized     *string `json:"model_normalized"`
	Timestamp           *string `json:"timestamp"`
	SessionID           *string `json:"session_id"`
	MessageID           *string `json:"message_id"`
	RequestID           *string `json:"request_id"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	OriginDeviceID      string  `json:"origin_device_id"`
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
		COALESCE(SUM(cost_usd), 0),
		MIN(timestamp_ms),
		MAX(timestamp_ms)
	FROM usage_events WHERE 1=1`
	var args []any
	query = addDateFilters(query, &args, filters, "timestamp_ms")

	var s Summary
	var firstMs, lastMs sql.NullInt64
	if err := conn.QueryRow(query, args...).Scan(
		&s.TotalEvents,
		&s.TotalTokens,
		&s.InputTokens,
		&s.OutputTokens,
		&s.CacheCreationTokens,
		&s.CacheReadTokens,
		&s.ReasoningTokens,
		&s.TotalCostUSD,
		&firstMs,
		&lastMs,
	); err != nil {
		return nil, err
	}
	if firstMs.Valid {
		s.FirstEventAt = stringPtr(formatMillis(firstMs.Int64))
	}
	if lastMs.Valid {
		s.LastEventAt = stringPtr(formatMillis(lastMs.Int64))
	}
	_ = conn.QueryRow("SELECT COUNT(*) FROM devices").Scan(&s.TotalDevices)
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

	query := fmt.Sprintf(`SELECT
		%s AS label,
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(cost_usd), 0)
	FROM usage_events WHERE 1=1`, labelExpr)
	var args []any
	query = addDateFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY label ASC"
	return scanMetricRows(conn, query, args...)
}

func BuildBreakdown(conn *sql.DB, by string, filters Filters) ([]MetricRow, error) {
	if by == "device" {
		query := `SELECT
			COALESCE(d.device_name || ' (' || substr(d.device_id, 1, 8) || ')', e.origin_device_id, 'unknown') AS label,
			COUNT(*),
			COALESCE(SUM(e.total_tokens), 0),
			COALESCE(SUM(e.input_tokens), 0),
			COALESCE(SUM(e.output_tokens), 0),
			COALESCE(SUM(e.cache_creation_tokens), 0),
			COALESCE(SUM(e.cache_read_tokens), 0),
			COALESCE(SUM(e.reasoning_tokens), 0),
			COALESCE(SUM(e.cost_usd), 0)
		FROM usage_events e LEFT JOIN devices d ON e.origin_device_id = d.device_id WHERE 1=1`
		var args []any
		query = addDateFilters(query, &args, filters, "e.timestamp_ms")
		query += " GROUP BY label ORDER BY COALESCE(SUM(e.total_tokens), 0) DESC, label ASC LIMIT 100"
		return scanMetricRows(conn, query, args...)
	}

	var labelExpr string
	switch by {
	case "agent", "":
		labelExpr = "COALESCE(agent, 'unknown')"
	case "model":
		labelExpr = "COALESCE(model_normalized, model_raw, 'unknown')"
	case "provider":
		labelExpr = "COALESCE(model_provider, provider, 'unknown')"
	default:
		return nil, fmt.Errorf("unsupported breakdown %q", by)
	}
	query := fmt.Sprintf(`SELECT
		%s AS label,
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(cost_usd), 0)
	FROM usage_events WHERE 1=1`, labelExpr)
	var args []any
	query = addDateFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT 100"
	return scanMetricRows(conn, query, args...)
}

func BuildSessions(conn *sql.DB, filters Filters, limit int) ([]MetricRow, error) {
	query := `SELECT
		COALESCE(session_id, 'no-session') AS label,
		COUNT(*),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_tokens), 0),
		COALESCE(SUM(cache_read_tokens), 0),
		COALESCE(SUM(reasoning_tokens), 0),
		COALESCE(SUM(cost_usd), 0)
	FROM usage_events WHERE 1=1`
	var args []any
	query = addDateFilters(query, &args, filters, "timestamp_ms")
	query += " GROUP BY label ORDER BY COALESCE(SUM(total_tokens), 0) DESC, label ASC LIMIT ?"
	args = append(args, limit)
	return scanMetricRows(conn, query, args...)
}

func ListImportRuns(conn *sql.DB, limit int) ([]ImportRun, error) {
	rows, err := conn.Query(`SELECT id, device_id, started_at_ms, finished_at_ms, status,
		COALESCE(files_scanned, 0), COALESCE(events_added, 0), COALESCE(events_skipped, 0), error
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
		if err := rows.Scan(&item.ID, &item.DeviceID, &started, &finished, &item.Status, &item.FilesScanned, &item.EventsAdded, &item.EventsSkipped, &errText); err != nil {
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
	query := `SELECT event_fingerprint, fingerprint_strategy, agent, provider, model_raw, model_normalized,
		timestamp_ms, session_id, message_id, request_id, COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		COALESCE(cache_creation_tokens, 0), COALESCE(cache_read_tokens, 0), COALESCE(reasoning_tokens, 0),
		COALESCE(total_tokens, 0), COALESCE(cost_usd, 0), origin_device_id
	FROM usage_events WHERE 1=1`
	var args []any
	query = addDateFilters(query, &args, filters, "timestamp_ms")
	query += " ORDER BY timestamp_ms DESC LIMIT ?"
	args = append(args, limit)
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Event
	for rows.Next() {
		var item Event
		var provider, modelRaw, modelNormalized, sessionID, messageID, requestID sql.NullString
		var ts sql.NullInt64
		if err := rows.Scan(
			&item.EventFingerprint,
			&item.FingerprintStrategy,
			&item.Agent,
			&provider,
			&modelRaw,
			&modelNormalized,
			&ts,
			&sessionID,
			&messageID,
			&requestID,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CacheCreationTokens,
			&item.CacheReadTokens,
			&item.ReasoningTokens,
			&item.TotalTokens,
			&item.CostUSD,
			&item.OriginDeviceID,
		); err != nil {
			return nil, err
		}
		item.Provider = nullableString(provider)
		item.ModelRaw = nullableString(modelRaw)
		item.ModelNormalized = nullableString(modelNormalized)
		item.SessionID = nullableString(sessionID)
		item.MessageID = nullableString(messageID)
		item.RequestID = nullableString(requestID)
		if ts.Valid {
			item.Timestamp = stringPtr(formatMillis(ts.Int64))
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func addDateFilters(query string, args *[]any, filters Filters, timestampExpr string) string {
	if filters.Since != "" {
		query += fmt.Sprintf(" AND %s >= ?", timestampExpr)
		*args = append(*args, dateStartMillis(filters.Since))
	}
	if filters.Until != "" {
		query += fmt.Sprintf(" AND %s < ?", timestampExpr)
		*args = append(*args, dateAfterMillis(filters.Until))
	}
	return query
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

func scanMetricRows(conn *sql.DB, query string, args ...any) ([]MetricRow, error) {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MetricRow
	for rows.Next() {
		var item MetricRow
		if err := rows.Scan(&item.Label, &item.Events, &item.TotalTokens, &item.InputTokens, &item.OutputTokens, &item.CacheCreationTokens, &item.CacheReadTokens, &item.ReasoningTokens, &item.CostUSD); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
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
