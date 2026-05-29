package report

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

type Filters struct {
	Since    string
	Until    string
	Channel  string
	Provider string
	Model    string
	Session  string
	Timezone string
	By       string
	SlowSort string
	Limit    int
}

type ReportRow struct {
	Label               string   `json:"label"`
	Events              int64    `json:"events"`
	TotalTokens         int64    `json:"total_tokens"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	AvgTotalDurationMs  *float64 `json:"avg_total_duration_ms"`
	AvgTTFTMs           *float64 `json:"avg_ttft_ms"`
	AvgOutputTPS        *float64 `json:"avg_output_tps"`
	RecordedCostUSD     float64  `json:"recorded_cost_usd"`
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
	Bucket              string   `json:"bucket"`
	Label               string   `json:"label"`
	Events              int64    `json:"events"`
	TotalTokens         int64    `json:"total_tokens"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CacheCreationTokens int64    `json:"cache_creation_tokens"`
	CacheReadTokens     int64    `json:"cache_read_tokens"`
	ReasoningTokens     int64    `json:"reasoning_tokens"`
	AvgTotalDurationMs  *float64 `json:"avg_total_duration_ms"`
	AvgTTFTMs           *float64 `json:"avg_ttft_ms"`
	AvgOutputTPS        *float64 `json:"avg_output_tps"`
	RecordedCostUSD     float64  `json:"recorded_cost_usd"`
}

func Generate(conn *sql.DB, reportType string, filters Filters, asJSON bool) error {
	if err := validateDateFilters(filters); err != nil {
		return err
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
	fmt.Fprintf(w, "Bucket\t%s\tEvents\tTokens\tInput\tOutput\tCache Create\tCache Read\tReasoning\tAvg TPS\tAvg TTFT(ms)\tCost(USD)\n", labelHeader)
	fmt.Fprintf(w, "---\t---\t---\t---\t---\t---\t---\t---\t---\t---\t---\t---\n")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t$%.4f\n",
			r.Bucket, truncate(r.Label, 40), r.Events, r.TotalTokens, r.InputTokens, r.OutputTokens,
			r.CacheCreationTokens, r.CacheReadTokens, r.ReasoningTokens,
			formatFloatPtr(r.AvgOutputTPS), formatFloatPtr(r.AvgTTFTMs), r.RecordedCostUSD)
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
	default:
		return "", "", fmt.Errorf("invalid time breakdown %q: expected channel, model, provider, or session", by)
	}
}

func generateGrouped(conn *sql.DB, labelExpr string, filters Filters, asJSON bool, labelHeader, order string) error {
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
	return executeReport(conn, query, args, asJSON, labelHeader)
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

func executeReport(conn *sql.DB, query string, args []any, asJSON bool, labelHeader string) error {
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
	fmt.Fprintf(w, "%s\tEvents\tTokens\tInput\tOutput\tCache Create\tCache Read\tReasoning\tAvg TPS\tAvg TTFT(ms)\tCost(USD)\n", labelHeader)
	fmt.Fprintf(w, "---\t---\t---\t---\t---\t---\t---\t---\t---\t---\t---\n")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t$%.4f\n",
			truncate(r.Label, 40), r.Events, r.TotalTokens, r.InputTokens, r.OutputTokens,
			r.CacheCreationTokens, r.CacheReadTokens, r.ReasoningTokens,
			formatFloatPtr(r.AvgOutputTPS), formatFloatPtr(r.AvgTTFTMs), r.RecordedCostUSD)
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
	return query
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
