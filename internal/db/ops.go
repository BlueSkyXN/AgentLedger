package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type ImportRun struct {
	ID            string
	StartedAtMs   int64
	FinishedAtMs  int64
	Status        string
	FilesScanned  int
	EventsAdded   int
	EventsUpdated int
	EventsSkipped int
}

func (d *Database) StartImportRun(runID string) error {
	_, err := d.conn.Exec(`
        INSERT INTO import_runs (id, started_at_ms, status)
        VALUES (?, ?, 'running')
    `, runID, time.Now().UnixMilli())
	return err
}

func (d *Database) FinishImportRun(runID string, filesScanned, eventsAdded, eventsUpdated, eventsSkipped int) error {
	_, err := d.conn.Exec(`
        UPDATE import_runs SET
            finished_at_ms = ?,
            status = 'completed',
            files_scanned = ?,
            events_added = ?,
            events_updated = ?,
            events_skipped = ?
        WHERE id = ?
    `, time.Now().UnixMilli(), filesScanned, eventsAdded, eventsUpdated, eventsSkipped, runID)
	return err
}

func (d *Database) UpsertEvent(ev *model.UsageEvent) (string, error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existing, err := selectEventForComparison(tx, ev.EventID)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == sql.ErrNoRows {
		if err = insertEvent(tx, ev); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "inserted", nil
	}

	if !isMoreComplete(ev, existing) {
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "skipped", nil
	}

	ev.ImportedAtMs = existing.ImportedAtMs
	if err = updateEvent(tx, ev); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return "updated", nil
}

func selectEventForComparison(tx *sql.Tx, eventID string) (*model.UsageEvent, error) {
	row := tx.QueryRow(`
        SELECT event_id, model_raw, model_normalized, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms,
            total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, imported_at_ms
        FROM usage_events WHERE event_id = ?
    `, eventID)
	var ev model.UsageEvent
	var requestStarted, firstToken, completed, totalDuration, ttft, outputDuration sql.NullInt64
	var outputTPS, recordedCost sql.NullFloat64
	if err := row.Scan(
		&ev.EventID,
		&ev.ModelRaw,
		&ev.ModelNormalized,
		&ev.TotalTokens,
		&requestStarted,
		&firstToken,
		&completed,
		&totalDuration,
		&ttft,
		&outputDuration,
		&outputTPS,
		&recordedCost,
		&ev.ImportedAtMs,
	); err != nil {
		return nil, err
	}
	ev.RequestStartedAtMs = nullInt64Ptr(requestStarted)
	ev.FirstTokenAtMs = nullInt64Ptr(firstToken)
	ev.CompletedAtMs = nullInt64Ptr(completed)
	ev.TotalDurationMs = nullInt64Ptr(totalDuration)
	ev.TTFTMs = nullInt64Ptr(ttft)
	ev.OutputDurationMs = nullInt64Ptr(outputDuration)
	ev.OutputTPS = nullFloat64Ptr(outputTPS)
	ev.RecordedCostUSD = nullFloat64Ptr(recordedCost)
	return &ev, nil
}

func isMoreComplete(candidate, existing *model.UsageEvent) bool {
	return completenessScore(candidate) > completenessScore(existing)
}

func completenessScore(ev *model.UsageEvent) int64 {
	var score int64
	if ev.RequestStartedAtMs != nil || ev.FirstTokenAtMs != nil || ev.CompletedAtMs != nil ||
		ev.TotalDurationMs != nil || ev.TTFTMs != nil || ev.OutputDurationMs != nil || ev.OutputTPS != nil {
		score += 1_000_000_000_000
	}
	if ev.RecordedCostUSD != nil {
		score += 100_000_000_000
	}
	if ev.ModelRaw != "" || ev.ModelNormalized != "" {
		score += 10_000_000_000
	}
	score += ev.TotalTokens
	return score
}

func insertEvent(exec interface {
	Exec(string, ...any) (sql.Result, error)
}, ev *model.UsageEvent) error {
	_, err := exec.Exec(`
        INSERT INTO usage_events (
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized,
            timestamp_ms, session_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        ) VALUES (
            ?, ?, ?,
            ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?, ?,
            ?, ?,
            ?, ?
        )
    `, eventArgs(ev)...)
	return err
}

func updateEvent(tx *sql.Tx, ev *model.UsageEvent) error {
	args := []any{
		ev.DedupeKey, ev.DedupeStrategy,
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized,
		ev.TimestampMs, ev.SessionID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens,
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, ev.RawUsageJSON,
		ev.UpdatedAtMs,
		ev.EventID,
	}
	_, err := tx.Exec(`
        UPDATE usage_events SET
            dedupe_key = ?, dedupe_strategy = ?,
            channel = ?, provider = ?, model_raw = ?, model_normalized = ?,
            timestamp_ms = ?, session_id = ?, project_path = ?, message_id = ?, request_id = ?, source_file = ?, line_number = ?, raw_sha256 = ?,
            input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, cache_creation_tokens = ?, cache_read_tokens = ?, total_tokens = ?,
            request_started_at_ms = ?, first_token_at_ms = ?, completed_at_ms = ?, total_duration_ms = ?, ttft_ms = ?, output_duration_ms = ?, output_tps = ?,
            recorded_cost_usd = ?, raw_usage_json = ?,
            updated_at_ms = ?
        WHERE event_id = ?
    `, args...)
	return err
}

func eventArgs(ev *model.UsageEvent) []any {
	return []any{
		ev.EventID, ev.DedupeKey, ev.DedupeStrategy,
		ev.Channel, ev.Provider, ev.ModelRaw, ev.ModelNormalized,
		ev.TimestampMs, ev.SessionID, ev.ProjectPath, ev.MessageID, ev.RequestID, ev.SourceFile, ev.LineNumber, ev.RawSHA256,
		ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationTokens, ev.CacheReadTokens, ev.TotalTokens,
		ev.RequestStartedAtMs, ev.FirstTokenAtMs, ev.CompletedAtMs, ev.TotalDurationMs, ev.TTFTMs, ev.OutputDurationMs, ev.OutputTPS,
		ev.RecordedCostUSD, ev.RawUsageJSON,
		ev.ImportedAtMs, ev.UpdatedAtMs,
	}
}

// MergeFrom attaches another v2 .aldb database and imports unseen events.
func (d *Database) MergeFrom(incomingPath string) (inserted int64, skipped int64, err error) {
	absPath, err := filepath.Abs(incomingPath)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot access file: %w", err)
	}
	if info.IsDir() {
		return 0, 0, fmt.Errorf("path is a directory, not a database file")
	}

	f, err := os.Open(absPath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot open file: %w", err)
	}
	header := make([]byte, 16)
	_, err = f.Read(header)
	_ = f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return 0, 0, fmt.Errorf("file is not a valid SQLite database")
	}

	escapedPath := strings.ReplaceAll(absPath, "'", "''")
	if _, err = d.conn.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS incoming", escapedPath)); err != nil {
		return 0, 0, fmt.Errorf("failed to attach incoming database: %w", err)
	}
	defer func() {
		_, _ = d.conn.Exec("DETACH DATABASE incoming")
	}()

	var version string
	if err = d.conn.QueryRow(`SELECT value FROM incoming.meta WHERE key='schema_version'`).Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("incoming database missing schema metadata: %w", err)
	}
	if version != SchemaVersion {
		return 0, 0, fmt.Errorf("incoming database schema version %s is not compatible with AgentLedger v2", version)
	}

	var totalIncoming int64
	if err = d.conn.QueryRow("SELECT COUNT(*) FROM incoming.usage_events").Scan(&totalIncoming); err != nil {
		return 0, 0, fmt.Errorf("failed to count incoming events: %w", err)
	}

	result, err := d.conn.Exec(`
        INSERT OR IGNORE INTO usage_events (
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized,
            timestamp_ms, session_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        )
        SELECT
            event_id, dedupe_key, dedupe_strategy,
            channel, provider, model_raw, model_normalized,
            timestamp_ms, session_id, project_path, message_id, request_id, source_file, line_number, raw_sha256,
            input_tokens, output_tokens, reasoning_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
            request_started_at_ms, first_token_at_ms, completed_at_ms, total_duration_ms, ttft_ms, output_duration_ms, output_tps,
            recorded_cost_usd, raw_usage_json,
            imported_at_ms, updated_at_ms
        FROM incoming.usage_events
    `)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to merge events: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, totalIncoming - rowsAffected, nil
}

func (d *Database) GetStats() (map[string]interface{}, error) {
	stats := map[string]interface{}{"schema_version": SchemaVersion}

	var count int64
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_events"] = count

	if err := d.conn.QueryRow("SELECT COUNT(*) FROM import_runs").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_import_runs"] = count

	var totalTokens sql.NullInt64
	if err := d.conn.QueryRow("SELECT SUM(total_tokens) FROM usage_events").Scan(&totalTokens); err != nil {
		return nil, err
	}
	stats["total_tokens"] = int64(0)
	if totalTokens.Valid {
		stats["total_tokens"] = totalTokens.Int64
	}

	var totalCost sql.NullFloat64
	if err := d.conn.QueryRow("SELECT SUM(recorded_cost_usd) FROM usage_events").Scan(&totalCost); err != nil {
		return nil, err
	}
	stats["total_recorded_cost_usd"] = 0.0
	if totalCost.Valid {
		stats["total_recorded_cost_usd"] = totalCost.Float64
	}

	return stats, nil
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}
