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

func (d *Database) UpsertDevice(dev *model.Device) error {
	_, err := d.conn.Exec(`
        INSERT INTO devices (device_id, device_name, hostname, os, arch, app_version, created_at_ms, last_seen_at_ms)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(device_id) DO UPDATE SET
            last_seen_at_ms = excluded.last_seen_at_ms,
            app_version = excluded.app_version
    `, dev.DeviceID, dev.DeviceName, dev.Hostname, dev.OS, dev.Arch, dev.AppVersion, dev.CreatedAtMs, dev.LastSeenAtMs)
	return err
}

type ImportRun struct {
	ID            string
	DeviceID      string
	StartedAtMs   int64
	FinishedAtMs  int64
	Status        string
	FilesScanned  int
	EventsAdded   int
	EventsSkipped int
}

func (d *Database) StartImportRun(runID, deviceID string) error {
	_, err := d.conn.Exec(`
        INSERT INTO import_runs (id, device_id, started_at_ms, status)
        VALUES (?, ?, ?, 'running')
    `, runID, deviceID, time.Now().UnixMilli())
	return err
}

func (d *Database) FinishImportRun(runID string, filesScanned, eventsAdded, eventsSkipped int) error {
	_, err := d.conn.Exec(`
        UPDATE import_runs SET
            finished_at_ms = ?,
            status = 'completed',
            files_scanned = ?,
            events_added = ?,
            events_skipped = ?
        WHERE id = ?
    `, time.Now().UnixMilli(), filesScanned, eventsAdded, eventsSkipped, runID)
	return err
}

func (d *Database) InsertEvent(ev *model.UsageEvent) (bool, error) {
	result, err := d.conn.Exec(`
        INSERT OR IGNORE INTO usage_events (
            event_fingerprint, dedupe_key, fingerprint_strategy,
            origin_device_id, first_seen_device_id, last_seen_device_id,
            agent, provider, client_name, source_channel, billing_channel, source_kind,
            model_raw, model_normalized, model_provider, model_family, is_fallback_model,
            speed_label, service_tier, speed_multiplier, is_fast_mode,
            timestamp_ms, timestamp_text, source_timezone, timestamp_offset_minutes,
            session_id, conversation_id, project, project_path_raw, project_path_normalized, workspace_key,
            message_id, request_id,
            input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
            reasoning_tokens, tool_tokens, extra_total_tokens, source_total_tokens, total_tokens,
            cost_usd, cost_source, pricing_source, pricing_version,
            credits, message_count,
            raw_usage_json, raw_meta_json, raw_sha256,
            created_at_ms, updated_at_ms
        ) VALUES (
            ?, ?, ?,
            ?, ?, ?,
            ?, ?, ?, ?, ?, ?,
            ?, ?, ?, ?, ?,
            ?, ?, ?, ?,
            ?, ?, ?, ?,
            ?, ?, ?, ?, ?, ?,
            ?, ?,
            ?, ?, ?, ?,
            ?, ?, ?, ?, ?,
            ?, ?, ?, ?,
            ?, ?,
            ?, ?, ?,
            ?, ?
        )
    `,
		ev.EventFingerprint, ev.DedupeKey, ev.FingerprintStrategy,
		ev.OriginDeviceID, ev.FirstSeenDeviceID, ev.LastSeenDeviceID,
		ev.Agent, ev.Provider, ev.ClientName, ev.SourceChannel, ev.BillingChannel, ev.SourceKind,
		ev.ModelRaw, ev.ModelNormalized, ev.ModelProvider, ev.ModelFamily, ev.IsFallbackModel,
		ev.SpeedLabel, ev.ServiceTier, ev.SpeedMultiplier, ev.IsFastMode,
		ev.TimestampMs, ev.TimestampText, ev.SourceTimezone, ev.TimestampOffsetMinutes,
		ev.SessionID, ev.ConversationID, ev.Project, ev.ProjectPathRaw, ev.ProjectPathNormalized, ev.WorkspaceKey,
		ev.MessageID, ev.RequestID,
		ev.InputTokens, ev.OutputTokens, ev.CacheCreationTokens, ev.CacheReadTokens,
		ev.ReasoningTokens, ev.ToolTokens, ev.ExtraTotalTokens, ev.SourceTotalTokens, ev.TotalTokens,
		ev.CostUSD, ev.CostSource, ev.PricingSource, ev.PricingVersion,
		ev.Credits, ev.MessageCount,
		ev.RawUsageJSON, ev.RawMetaJSON, ev.RawSHA256,
		ev.CreatedAtMs, ev.UpdatedAtMs,
	)
	if err != nil {
		return false, err
	}

	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// MergeFrom attaches another .aldb database and merges events.
func (d *Database) MergeFrom(incomingPath string, deviceID string) (inserted int64, skipped int64, err error) {
	nowMs := time.Now().UnixMilli()

	// Validate the incoming path: must exist and be a regular file
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

	// Verify SQLite header
	f, err := os.Open(absPath)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot open file: %w", err)
	}
	header := make([]byte, 16)
	_, err = f.Read(header)
	f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return 0, 0, fmt.Errorf("file is not a valid SQLite database")
	}

	escapedPath := strings.ReplaceAll(absPath, "'", "''")
	_, err = d.conn.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS incoming", escapedPath))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to attach incoming database: %w", err)
	}
	defer func() {
		_, _ = d.conn.Exec("DETACH DATABASE incoming")
	}()

	var totalIncoming int64
	err = d.conn.QueryRow("SELECT COUNT(*) FROM incoming.usage_events").Scan(&totalIncoming)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count incoming events: %w", err)
	}

	result, err := d.conn.Exec(fmt.Sprintf(`
        INSERT OR IGNORE INTO usage_events (
            event_fingerprint, dedupe_key, fingerprint_strategy,
            origin_device_id, first_seen_device_id, last_seen_device_id,
            agent, provider, client_name, source_channel, billing_channel, source_kind,
            model_raw, model_normalized, model_provider, model_family, is_fallback_model,
            speed_label, service_tier, speed_multiplier, is_fast_mode,
            timestamp_ms, timestamp_text, source_timezone, timestamp_offset_minutes,
            session_id, conversation_id, project, project_path_raw, project_path_normalized, workspace_key,
            message_id, request_id,
            input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
            reasoning_tokens, tool_tokens, extra_total_tokens, source_total_tokens, total_tokens,
            cost_usd, cost_source, pricing_source, pricing_version,
            credits, message_count,
            raw_usage_json, raw_meta_json, raw_sha256,
            created_at_ms, updated_at_ms
        )
        SELECT
            event_fingerprint, dedupe_key, fingerprint_strategy,
            origin_device_id, first_seen_device_id, '%s',
            agent, provider, client_name, source_channel, billing_channel, source_kind,
            model_raw, model_normalized, model_provider, model_family, is_fallback_model,
            speed_label, service_tier, speed_multiplier, is_fast_mode,
            timestamp_ms, timestamp_text, source_timezone, timestamp_offset_minutes,
            session_id, conversation_id, project, project_path_raw, project_path_normalized, workspace_key,
            message_id, request_id,
            input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
            reasoning_tokens, tool_tokens, extra_total_tokens, source_total_tokens, total_tokens,
            cost_usd, cost_source, pricing_source, pricing_version,
            credits, message_count,
            raw_usage_json, raw_meta_json, raw_sha256,
            created_at_ms, %d
        FROM incoming.usage_events
    `, strings.ReplaceAll(deviceID, "'", "''"), nowMs))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to merge events: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, totalIncoming - rowsAffected, nil
}

// GetStats returns database statistics.
func (d *Database) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var count int64
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_events"] = count

	if err := d.conn.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_devices"] = count

	if err := d.conn.QueryRow("SELECT COUNT(*) FROM import_runs").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_import_runs"] = count

	if err := d.conn.QueryRow("SELECT COUNT(*) FROM source_files").Scan(&count); err != nil {
		return nil, err
	}
	stats["total_source_files"] = count

	var totalTokens sql.NullInt64
	if err := d.conn.QueryRow("SELECT SUM(total_tokens) FROM usage_events").Scan(&totalTokens); err != nil {
		return nil, err
	}
	if totalTokens.Valid {
		stats["total_tokens"] = totalTokens.Int64
	} else {
		stats["total_tokens"] = int64(0)
	}

	var totalCost sql.NullFloat64
	if err := d.conn.QueryRow("SELECT SUM(cost_usd) FROM usage_events").Scan(&totalCost); err != nil {
		return nil, err
	}
	if totalCost.Valid {
		stats["total_cost_usd"] = totalCost.Float64
	} else {
		stats["total_cost_usd"] = 0.0
	}

	return stats, nil
}
