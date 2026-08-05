package analytics

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/pricing"
)

type Filters struct {
	Since         string
	Until         string
	Channel       string
	SourceProduct string
	Provider      string
	Model         string
	Session       string
	Project       string
	Timezone      string
	CostMode      string
	PricingPath   string
}

type PricingInfo struct {
	Status           string  `json:"status"`
	ErrorCode        string  `json:"error_code,omitempty"`
	ProfileID        string  `json:"profile_id,omitempty"`
	PricedEvents     int64   `json:"priced_events"`
	UnpricedEvents   int64   `json:"unpriced_events"`
	PolicyZeroEvents int64   `json:"policy_zero_events"`
	PricedTokens     int64   `json:"priced_tokens"`
	UnpricedTokens   int64   `json:"unpriced_tokens"`
	PolicyZeroTokens int64   `json:"policy_zero_tokens"`
	EventCoverage    float64 `json:"event_coverage_ratio"`
	TokenCoverage    float64 `json:"token_coverage_ratio"`
	Confidence       string  `json:"confidence,omitempty"`
}

type Summary struct {
	TotalEvents         int64        `json:"total_events"`
	TotalSessions       int64        `json:"total_sessions"`
	ImportRuns          int64        `json:"import_runs"`
	TotalTokens         int64        `json:"total_tokens"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	CacheCreationTokens int64        `json:"cache_creation_tokens"`
	CacheReadTokens     int64        `json:"cache_read_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens"`
	FirstDate           *string      `json:"first_date"`
	LastDate            *string      `json:"last_date"`
	EstimatedCostUSD    *float64     `json:"estimated_cost_usd"`
	Pricing             *PricingInfo `json:"pricing"`
}

type MetricRow struct {
	Label               string       `json:"label"`
	Events              int64        `json:"events"`
	TotalTokens         int64        `json:"total_tokens"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	CacheCreationTokens int64        `json:"cache_creation_tokens"`
	CacheReadTokens     int64        `json:"cache_read_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens"`
	EstimatedCostUSD    *float64     `json:"estimated_cost_usd"`
	Pricing             *PricingInfo `json:"pricing"`
}

type ImportRun struct {
	ID             string  `json:"id"`
	StartedAt      string  `json:"started_at"`
	FinishedAt     *string `json:"finished_at"`
	StartedAtMs    int64   `json:"started_at_ms"`
	FinishedAtMs   *int64  `json:"finished_at_ms"`
	Status         string  `json:"status"`
	FilesScanned   int64   `json:"files_scanned"`
	EventsAdded    int64   `json:"events_added"`
	EventsUpdated  int64   `json:"events_updated"`
	EventsSkipped  int64   `json:"events_skipped"`
	EventsRejected int64   `json:"events_rejected"`
	Error          *string `json:"error,omitempty"`
}

type Event struct {
	EventID             string  `json:"event_id"`
	IdentityStrategy    string  `json:"identity_strategy"`
	IdentityScope       string  `json:"identity_scope"`
	EventGranularity    string  `json:"event_granularity"`
	Channel             string  `json:"channel"`
	SourceProduct       string  `json:"source_product"`
	Provider            *string `json:"provider"`
	ModelRaw            *string `json:"model_raw"`
	ModelNormalized     string  `json:"model_normalized"`
	ModelResolution     *string `json:"model_resolution"`
	ModelIsFallback     bool    `json:"model_is_fallback"`
	TimestampMs         int64   `json:"timestamp_ms"`
	Timestamp           string  `json:"timestamp"`
	SessionKey          string  `json:"session_key"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	AccountingProfile   *string `json:"accounting_profile"`
	ObservabilityLevel  *string `json:"observability_level"`
}

type Session struct {
	SessionKey          string       `json:"session_key"`
	SessionID           *string      `json:"session_id"`
	Channel             string       `json:"channel"`
	SourceProduct       string       `json:"source_product"`
	FirstDate           string       `json:"first_date"`
	LastDate            string       `json:"last_date"`
	EventCount          int64        `json:"event_count"`
	PrimaryModel        string       `json:"primary_model"`
	ModelCount          int64        `json:"model_count"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens"`
	CacheCreationTokens int64        `json:"cache_creation_tokens"`
	CacheReadTokens     int64        `json:"cache_read_tokens"`
	TotalTokens         int64        `json:"total_tokens"`
	EstimatedCostUSD    *float64     `json:"estimated_cost_usd"`
	Pricing             *PricingInfo `json:"pricing"`
	PricedEvents        int64        `json:"priced_events"`
	UnpricedEvents      int64        `json:"unpriced_events"`
	PolicyZeroEvents    int64        `json:"policy_zero_events"`
}

type PaginatedSessions struct {
	Items  []Session `json:"items"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
	Total  int64     `json:"total"`
}

type PaginatedEvents struct {
	Items  []Event `json:"items"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
	Total  int64   `json:"total"`
}

type FilterOptions struct {
	Channels       []string `json:"channels"`
	SourceProducts []string `json:"source_products"`
	Providers      []string `json:"providers"`
	Models         []string `json:"models"`
	Sessions       []string `json:"sessions"`
	Projects       []string `json:"projects"`
}

func BuildSummary(conn *sql.DB, filters Filters) (*Summary, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	query := `SELECT
        COUNT(*), COUNT(DISTINCT session_key),
        COALESCE(SUM(total_tokens), 0), COALESCE(SUM(input_tokens), 0),
        COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0),
        COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(reasoning_tokens), 0),
        MIN(timestamp_ms), MAX(timestamp_ms)
        FROM usage_events WHERE 1=1`
	args := []any{}
	query = addFilters(query, &args, filters, "timestamp_ms")
	var summary Summary
	var first, last sql.NullInt64
	if err := conn.QueryRow(query, args...).Scan(
		&summary.TotalEvents, &summary.TotalSessions,
		&summary.TotalTokens, &summary.InputTokens, &summary.OutputTokens,
		&summary.CacheCreationTokens, &summary.CacheReadTokens, &summary.ReasoningTokens,
		&first, &last,
	); err != nil {
		return nil, err
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM import_runs`).Scan(&summary.ImportRuns); err != nil {
		return nil, err
	}
	if first.Valid {
		label, err := bucketLabel(conn, first.Int64, filters.Timezone, "daily")
		if err != nil {
			return nil, err
		}
		summary.FirstDate = &label
	}
	if last.Valid {
		label, err := bucketLabel(conn, last.Int64, filters.Timezone, "daily")
		if err != nil {
			return nil, err
		}
		summary.LastDate = &label
	}
	estimates := estimateCosts(conn, filters, `''`)
	attachEstimate(&summary.EstimatedCostUSD, &summary.Pricing, estimates[""])
	return &summary, nil
}

func BuildTimeseries(conn *sql.DB, bucket string, filters Filters) ([]MetricRow, error) {
	if bucket != "daily" && bucket != "weekly" && bucket != "monthly" {
		return nil, fmt.Errorf("unsupported bucket %q", bucket)
	}
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	labelExpr := `agentledger_time_bucket(timestamp_ms, ?, ?)`
	args := []any{effectiveTimezone(filters.Timezone), bucket}
	rows, err := groupedRows(conn, labelExpr, filters, args)
	if err != nil {
		return nil, err
	}
	estimates := estimateCostsWithPrefix(conn, filters, labelExpr, []any{effectiveTimezone(filters.Timezone), bucket})
	for index := range rows {
		attachEstimate(&rows[index].EstimatedCostUSD, &rows[index].Pricing, estimateForLabel(estimates, rows[index].Label))
	}
	return rows, nil
}

func BuildBreakdown(conn *sql.DB, by string, filters Filters) ([]MetricRow, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	labelExpr, err := breakdownExpr(by)
	if err != nil {
		return nil, err
	}
	rows, err := groupedRows(conn, labelExpr, filters, nil)
	if err != nil {
		return nil, err
	}
	estimates := estimateCosts(conn, filters, labelExpr)
	for index := range rows {
		attachEstimate(&rows[index].EstimatedCostUSD, &rows[index].Pricing, estimateForLabel(estimates, rows[index].Label))
	}
	return rows, nil
}

func groupedRows(conn *sql.DB, labelExpr string, filters Filters, prefixArgs []any) ([]MetricRow, error) {
	query := `SELECT ` + labelExpr + ` AS label,
        COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(input_tokens), 0),
        COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_creation_tokens), 0),
        COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(reasoning_tokens), 0)
        FROM usage_events WHERE 1=1`
	args := append([]any{}, prefixArgs...)
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += ` GROUP BY label ORDER BY label`
	dbRows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()
	rows := make([]MetricRow, 0)
	for dbRows.Next() {
		var row MetricRow
		if err := dbRows.Scan(&row.Label, &row.Events, &row.TotalTokens, &row.InputTokens, &row.OutputTokens, &row.CacheCreationTokens, &row.CacheReadTokens, &row.ReasoningTokens); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, dbRows.Err()
}

func BuildSessions(conn *sql.DB, filters Filters, limit, offset int) (*PaginatedSessions, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	whereArgs := []any{}
	where := addFilters(" WHERE 1=1", &whereArgs, filters, "timestamp_ms")
	var total int64
	if err := conn.QueryRow(`SELECT COUNT(DISTINCT session_key) FROM usage_events`+where, whereArgs...).Scan(&total); err != nil {
		return nil, err
	}

	query := `WITH filtered AS (
        SELECT * FROM usage_events` + where + `
    ), model_totals AS (
        SELECT session_key, model_normalized, SUM(total_tokens) AS model_tokens
        FROM filtered GROUP BY session_key, model_normalized
    ), ranked_models AS (
        SELECT session_key, model_normalized,
               ROW_NUMBER() OVER (PARTITION BY session_key ORDER BY model_tokens DESC, model_normalized ASC) AS model_rank
        FROM model_totals
    )
    SELECT f.session_key, NULLIF(MIN(COALESCE(f.session_id, '')), ''), MIN(f.channel), MIN(f.source_product),
           agentledger_time_bucket(MIN(f.timestamp_ms), ?, 'daily'),
           agentledger_time_bucket(MAX(f.timestamp_ms), ?, 'daily'),
           COUNT(*), rm.model_normalized, COUNT(DISTINCT f.model_normalized),
           SUM(f.input_tokens), SUM(f.output_tokens), SUM(f.reasoning_tokens),
           SUM(f.cache_creation_tokens), SUM(f.cache_read_tokens), SUM(f.total_tokens)
    FROM filtered f
    JOIN ranked_models rm ON rm.session_key=f.session_key AND rm.model_rank=1
    GROUP BY f.session_key, rm.model_normalized
    ORDER BY MAX(f.timestamp_ms) DESC, f.session_key ASC
    LIMIT ? OFFSET ?`
	args := append([]any{}, whereArgs...)
	args = append(args, effectiveTimezone(filters.Timezone), effectiveTimezone(filters.Timezone), limit, offset)
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Session, 0)
	for rows.Next() {
		var item Session
		var sessionID sql.NullString
		if err := rows.Scan(
			&item.SessionKey, &sessionID, &item.Channel, &item.SourceProduct,
			&item.FirstDate, &item.LastDate, &item.EventCount, &item.PrimaryModel, &item.ModelCount,
			&item.InputTokens, &item.OutputTokens, &item.ReasoningTokens,
			&item.CacheCreationTokens, &item.CacheReadTokens, &item.TotalTokens,
		); err != nil {
			return nil, err
		}
		item.SessionID = nullableString(sessionID)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	estimates := estimateCosts(conn, filters, `session_key`)
	for index := range items {
		attachEstimate(&items[index].EstimatedCostUSD, &items[index].Pricing, estimateForLabel(estimates, items[index].SessionKey))
		if items[index].Pricing != nil {
			items[index].PricedEvents = items[index].Pricing.PricedEvents
			items[index].UnpricedEvents = items[index].Pricing.UnpricedEvents
			items[index].PolicyZeroEvents = items[index].Pricing.PolicyZeroEvents
		}
	}
	return &PaginatedSessions{Items: items, Limit: limit, Offset: offset, Total: total}, nil
}

func ListEvents(conn *sql.DB, filters Filters, limit, offset int) (*PaginatedEvents, error) {
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT COUNT(*) FROM usage_events WHERE 1=1`
	args := []any{}
	query = addFilters(query, &args, filters, "timestamp_ms")
	var total int64
	if err := conn.QueryRow(query, args...).Scan(&total); err != nil {
		return nil, err
	}

	query = `SELECT event_id, identity_strategy, identity_scope, event_granularity,
        channel, source_product, provider, model_raw, model_normalized, model_resolution, model_is_fallback,
        timestamp_ms, session_key,
        input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
        accounting_profile, observability_level
        FROM usage_events WHERE 1=1`
	args = []any{}
	query = addFilters(query, &args, filters, "timestamp_ms")
	query += ` ORDER BY timestamp_ms DESC, event_id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var item Event
		var provider, modelRaw, modelResolution, accountingProfile, observability sql.NullString
		var fallback int
		if err := rows.Scan(
			&item.EventID, &item.IdentityStrategy, &item.IdentityScope, &item.EventGranularity,
			&item.Channel, &item.SourceProduct, &provider, &modelRaw, &item.ModelNormalized, &modelResolution, &fallback,
			&item.TimestampMs, &item.SessionKey,
			&item.InputTokens, &item.OutputTokens, &item.ReasoningTokens, &item.CacheCreationTokens, &item.CacheReadTokens, &item.TotalTokens,
			&accountingProfile, &observability,
		); err != nil {
			return nil, err
		}
		item.Provider = nullableString(provider)
		item.ModelRaw = nullableString(modelRaw)
		item.ModelResolution = nullableString(modelResolution)
		item.ModelIsFallback = fallback != 0
		item.AccountingProfile = nullableString(accountingProfile)
		item.ObservabilityLevel = nullableString(observability)
		item.Timestamp = time.UnixMilli(item.TimestampMs).UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &PaginatedEvents{Items: items, Limit: limit, Offset: offset, Total: total}, nil
}

func ListImportRuns(conn *sql.DB, limit int) ([]ImportRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := conn.Query(`SELECT id, started_at_ms, finished_at_ms, status, files_scanned,
        events_added, events_updated, events_skipped, events_rejected, error
        FROM import_runs ORDER BY started_at_ms DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ImportRun, 0)
	for rows.Next() {
		var item ImportRun
		var finished sql.NullInt64
		var warning sql.NullString
		if err := rows.Scan(&item.ID, &item.StartedAtMs, &finished, &item.Status, &item.FilesScanned,
			&item.EventsAdded, &item.EventsUpdated, &item.EventsSkipped, &item.EventsRejected, &warning); err != nil {
			return nil, err
		}
		item.FinishedAtMs = nullableInt(finished)
		item.StartedAt = time.UnixMilli(item.StartedAtMs).UTC().Format(time.RFC3339Nano)
		if finished.Valid {
			formatted := time.UnixMilli(finished.Int64).UTC().Format(time.RFC3339Nano)
			item.FinishedAt = &formatted
		}
		item.Error = nullableString(warning)
		items = append(items, item)
	}
	return items, rows.Err()
}

func BuildFilterOptions(conn *sql.DB) (*FilterOptions, error) {
	options := &FilterOptions{}
	var err error
	if options.Channels, err = distinctStrings(conn, `channel`); err != nil {
		return nil, err
	}
	if options.SourceProducts, err = distinctStrings(conn, `source_product`); err != nil {
		return nil, err
	}
	if options.Providers, err = distinctStrings(conn, `provider`); err != nil {
		return nil, err
	}
	if options.Models, err = distinctStrings(conn, `model_normalized`); err != nil {
		return nil, err
	}
	if options.Sessions, err = distinctStrings(conn, `session_key`); err != nil {
		return nil, err
	}
	if options.Projects, err = distinctStrings(conn, `agentledger_project_label(project_path)`); err != nil {
		return nil, err
	}
	return options, nil
}

func distinctStrings(conn *sql.DB, expression string) ([]string, error) {
	rows, err := conn.Query(`SELECT DISTINCT ` + expression + ` AS value FROM usage_events
        WHERE ` + expression + ` IS NOT NULL AND TRIM(` + expression + `) <> '' ORDER BY value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func breakdownExpr(by string) (string, error) {
	switch by {
	case "channel":
		return `channel`, nil
	case "source", "source_product":
		return `source_product`, nil
	case "provider":
		return `COALESCE(NULLIF(provider, ''), 'unknown')`, nil
	case "model":
		return `model_normalized`, nil
	case "session":
		return `session_key`, nil
	case "project":
		return `agentledger_project_label(project_path)`, nil
	default:
		return "", fmt.Errorf("unsupported breakdown dimension %q", by)
	}
}

func addFilters(query string, args *[]any, filters Filters, timestampExpr string) string {
	if filters.Since != "" {
		query += ` AND ` + timestampExpr + ` >= ?`
		*args = append(*args, dateStartMillis(filters.Since, filters.Timezone))
	}
	if filters.Until != "" {
		query += ` AND ` + timestampExpr + ` < ?`
		*args = append(*args, dateAfterMillis(filters.Until, filters.Timezone))
	}
	if filters.Channel != "" {
		query += ` AND channel = ?`
		*args = append(*args, filters.Channel)
	}
	if filters.SourceProduct != "" {
		query += ` AND source_product = ?`
		*args = append(*args, filters.SourceProduct)
	}
	if filters.Provider != "" {
		query += ` AND provider = ?`
		*args = append(*args, filters.Provider)
	}
	if filters.Model != "" {
		query += ` AND model_normalized = ?`
		*args = append(*args, filters.Model)
	}
	if filters.Session != "" {
		query += ` AND (session_key = ? OR session_id = ?)`
		*args = append(*args, filters.Session, filters.Session)
	}
	if filters.Project != "" {
		query += ` AND agentledger_project_label(project_path) = ?`
		*args = append(*args, filters.Project)
	}
	return query
}

func validateFilters(filters Filters) error {
	if _, err := time.LoadLocation(effectiveTimezone(filters.Timezone)); err != nil {
		return fmt.Errorf("invalid timezone %q", filters.Timezone)
	}
	if err := validateDate("since", filters.Since); err != nil {
		return err
	}
	return validateDate("until", filters.Until)
}

func validateDate(name, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return nil
	}
	return fmt.Errorf("invalid %s value %q", name, value)
}

func effectiveTimezone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Local"
	}
	return value
}

func dateStartMillis(value, timezone string) int64 {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UnixMilli()
	}
	location, _ := time.LoadLocation(effectiveTimezone(timezone))
	parsed, _ := time.ParseInLocation("2006-01-02", value, location)
	return parsed.UnixMilli()
}

func dateAfterMillis(value, timezone string) int64 {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UnixMilli() + 1
	}
	location, _ := time.LoadLocation(effectiveTimezone(timezone))
	parsed, _ := time.ParseInLocation("2006-01-02", value, location)
	return parsed.AddDate(0, 0, 1).UnixMilli()
}

func bucketLabel(conn *sql.DB, timestamp int64, timezone, bucket string) (string, error) {
	var label string
	err := conn.QueryRow(`SELECT agentledger_time_bucket(?, ?, ?)`, timestamp, effectiveTimezone(timezone), bucket).Scan(&label)
	return label, err
}

type estimateResult struct {
	cost    *float64
	pricing *PricingInfo
}

func estimateCosts(conn *sql.DB, filters Filters, labelExpr string) map[string]estimateResult {
	return estimateCostsWithPrefix(conn, filters, labelExpr, nil)
}

func estimateCostsWithPrefix(conn *sql.DB, filters Filters, labelExpr string, prefixArgs []any) map[string]estimateResult {
	results := make(map[string]estimateResult)
	if strings.EqualFold(filters.CostMode, "none") {
		return results
	}
	estimator, profile, info := loadEstimator(filters.PricingPath)
	if estimator == nil {
		results[""] = estimateResult{pricing: info}
		return results
	}
	query := `SELECT ` + labelExpr + ` AS label, timestamp_ms, channel, COALESCE(provider, ''),
        model_normalized, source_product, COALESCE(observability_level, ''),
        COALESCE(token_accounting_method, ''), COALESCE(accounting_profile, ''),
        input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, reasoning_tokens, total_tokens
        FROM usage_events WHERE 1=1`
	args := append([]any{}, prefixArgs...)
	query = addFilters(query, &args, filters, "timestamp_ms")
	rows, err := conn.Query(query, args...)
	if err != nil {
		results[""] = estimateResult{pricing: unavailablePricing("pricing_query_failed")}
		return results
	}
	defer rows.Close()
	aggregates := make(map[string]*pricing.AggregateCost)
	for rows.Next() {
		var label string
		var event pricing.Event
		if err := rows.Scan(&label, &event.TimestampMs, &event.Channel, &event.Provider,
			&event.Model, &event.SourceProduct, &event.ObservabilityLevel,
			&event.TokenAccountingMethod, &event.AccountingProfile,
			&event.InputTokens, &event.OutputTokens, &event.CacheCreationTokens,
			&event.CacheReadTokens, &event.ReasoningTokens, &event.TotalTokens); err != nil {
			results[""] = estimateResult{pricing: unavailablePricing("pricing_query_failed")}
			return results
		}
		estimate, err := estimator.Estimate(event)
		if err != nil {
			results[""] = estimateResult{pricing: unavailablePricing("pricing_estimation_failed")}
			return results
		}
		aggregate := aggregates[label]
		if aggregate == nil {
			aggregate = &pricing.AggregateCost{}
			aggregates[label] = aggregate
		}
		aggregate.Add(event, estimate)
	}
	if err := rows.Err(); err != nil {
		results[""] = estimateResult{pricing: unavailablePricing("pricing_query_failed")}
		return results
	}
	for label, aggregate := range aggregates {
		cost := pricing.MicroUSDToUSD(aggregate.EstimatedCostMicroUSD)
		results[label] = estimateResult{cost: &cost, pricing: pricingInfo(profile, aggregate.Summary(profile))}
	}
	if len(aggregates) == 0 {
		zero := 0.0
		results[""] = estimateResult{cost: &zero, pricing: pricingInfo(profile, nil)}
	}
	return results
}

func loadEstimator(path string) (*pricing.Estimator, *pricing.Profile, *PricingInfo) {
	var profile *pricing.Profile
	var err error
	if strings.TrimSpace(path) == "" {
		profile, err = pricing.LoadDefaultProfile()
	} else {
		profile, err = pricing.LoadProfileFile(path)
	}
	if err != nil {
		return nil, nil, unavailablePricing("pricing_profile_invalid")
	}
	estimator, err := pricing.NewEstimator(profile)
	if err != nil {
		return nil, nil, unavailablePricing("pricing_profile_invalid")
	}
	return estimator, profile, nil
}

func unavailablePricing(code string) *PricingInfo {
	return &PricingInfo{Status: "unavailable", ErrorCode: code}
}

func pricingInfo(profile *pricing.Profile, coverage *pricing.CoverageSummary) *PricingInfo {
	info := &PricingInfo{Status: "available", ProfileID: profile.ID}
	if coverage == nil {
		return info
	}
	info.PricedEvents = coverage.PricedEvents
	info.PolicyZeroEvents = coverage.PolicyZeroEvents
	info.UnpricedEvents = coverage.TotalEvents - coverage.PricedEvents - coverage.PolicyZeroEvents
	info.PricedTokens = coverage.PricedTokens
	info.PolicyZeroTokens = coverage.PolicyZeroTokens
	info.UnpricedTokens = coverage.TotalTokens - coverage.PricedTokens - coverage.PolicyZeroTokens
	info.EventCoverage = coverage.EventCoverageRatio
	info.TokenCoverage = coverage.TokenCoverageRatio
	info.Confidence = coverage.Confidence
	return info
}

func attachEstimate(cost **float64, info **PricingInfo, result estimateResult) {
	*cost = result.cost
	*info = result.pricing
}

func estimateForLabel(results map[string]estimateResult, label string) estimateResult {
	if result, ok := results[label]; ok {
		return result
	}
	return results[""]
}

func nullableString(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	copy := value.String
	return &copy
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}
