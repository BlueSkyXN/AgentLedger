package db

import (
	"errors"
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/BlueSkyXN/AgentLedger/internal/usageevidence"
)

const rawEvidenceBatchSize = 1000

// RawEvidenceStats is an aggregate-only compact-evidence result. It never
// includes a raw envelope, event identifier, or source location.
type RawEvidenceStats struct {
	Candidates          int64
	AlreadyCompacted    int64
	Empty               int64
	UnknownPreserved    int64
	IdentityProtected   int64
	RawBytesBefore      int64
	RawBytesAfter       int64
	Updated             int64
	BatchesCompleted    int64
	RemainingCandidates int64
}

type rawEvidenceCandidate struct {
	eventID string
	raw     string
	compact string
}

func compactEvidence(channel, raw string) (usageevidence.Result, error) {
	result := usageevidence.Compact(channel, raw)
	if result.Status == usageevidence.StatusInternalError {
		return result, errors.New("raw usage evidence compaction failed")
	}
	return result, nil
}

func sqliteCompactEvidence(channel, raw string) (any, error) {
	result, err := compactEvidence(channel, raw)
	if err != nil {
		return nil, err
	}
	switch result.Status {
	case usageevidence.StatusRecognizedLegacy, usageevidence.StatusAlreadyCompact:
		return result.JSON, nil
	case usageevidence.StatusEmpty, usageevidence.StatusUnknown:
		return nil, nil
	default:
		return nil, errors.New("raw usage evidence compaction failed")
	}
}

func sqliteEvidenceStatus(channel, raw string) (string, error) {
	result, err := compactEvidence(channel, raw)
	if err != nil {
		return "", err
	}
	return string(result.Status), nil
}

func rawEvidenceIdentityProtected(strategy string) bool {
	return strategy == "raw_hash" || strategy == "fallback"
}

func addRawEvidenceStats(stats *RawEvidenceStats, channel, strategy, raw string) (rawEvidenceCandidate, error) {
	stats.RawBytesBefore += int64(len(raw))
	if rawEvidenceIdentityProtected(strategy) {
		stats.IdentityProtected++
		stats.RawBytesAfter += int64(len(raw))
		return rawEvidenceCandidate{}, nil
	}

	result, err := compactEvidence(channel, raw)
	if err != nil {
		return rawEvidenceCandidate{}, err
	}
	switch result.Status {
	case usageevidence.StatusRecognizedLegacy:
		stats.Candidates++
		stats.RawBytesAfter += int64(len(result.JSON))
		return rawEvidenceCandidate{raw: raw, compact: result.JSON}, nil
	case usageevidence.StatusAlreadyCompact:
		stats.AlreadyCompacted++
		stats.RawBytesAfter += int64(len(raw))
	case usageevidence.StatusEmpty:
		stats.Empty++
	case usageevidence.StatusUnknown:
		stats.UnknownPreserved++
		stats.RawBytesAfter += int64(len(raw))
	default:
		return rawEvidenceCandidate{}, errors.New("raw usage evidence compaction failed")
	}
	return rawEvidenceCandidate{}, nil
}

// InspectRawEvidence computes a no-write compact-evidence plan. It is safe to
// call through OpenReadOnlyV2 for a CLI dry-run.
func (d *Database) InspectRawEvidence() (RawEvidenceStats, error) {
	var stats RawEvidenceStats
	rows, err := d.conn.Query(`
		SELECT channel, dedupe_strategy, COALESCE(raw_usage_json, '')
		FROM usage_events
		ORDER BY event_id
	`)
	if err != nil {
		return stats, fmt.Errorf("read raw usage evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var channel, strategy, raw string
		if err := rows.Scan(&channel, &strategy, &raw); err != nil {
			return stats, fmt.Errorf("read raw usage evidence: %w", err)
		}
		if _, err := addRawEvidenceStats(&stats, channel, strategy, raw); err != nil {
			return stats, err
		}
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("read raw usage evidence: %w", err)
	}
	return stats, nil
}

// CompactRawEvidence rewrites only recognized, non-identity raw evidence in
// atomic keyset batches. Already compact, empty, unknown, raw-hash, and
// fallback evidence remains byte-for-byte unchanged.
func (d *Database) CompactRawEvidence() (RawEvidenceStats, error) {
	stats, err := d.InspectRawEvidence()
	if err != nil {
		return stats, err
	}
	if stats.Candidates == 0 {
		stats.RemainingCandidates = 0
		return stats, nil
	}
	if _, err := d.conn.Exec(`PRAGMA secure_delete = ON`); err != nil {
		return stats, fmt.Errorf("enable secure delete: %w", err)
	}

	var lastRowID int64
	for {
		rows, err := d.conn.Query(`
			SELECT rowid, event_id, channel, dedupe_strategy, COALESCE(raw_usage_json, '')
			FROM usage_events
			WHERE rowid > ?
			ORDER BY rowid
			LIMIT ?
		`, lastRowID, rawEvidenceBatchSize)
		if err != nil {
			stats.RemainingCandidates = remainingCandidateEstimate(stats)
			return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
		}

		var candidates []rawEvidenceCandidate
		var rowCount int
		for rows.Next() {
			var rowID int64
			var eventID, channel, strategy, raw string
			if err := rows.Scan(&rowID, &eventID, &channel, &strategy, &raw); err != nil {
				_ = rows.Close()
				stats.RemainingCandidates = remainingCandidateEstimate(stats)
				return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
			}
			lastRowID = rowID
			rowCount++
			candidate, err := rawEvidenceCandidateForRow(eventID, channel, strategy, raw)
			if err != nil {
				_ = rows.Close()
				stats.RemainingCandidates = remainingCandidateEstimate(stats)
				return stats, err
			}
			if candidate.eventID != "" {
				candidates = append(candidates, candidate)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			stats.RemainingCandidates = remainingCandidateEstimate(stats)
			return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
		}
		if err := rows.Close(); err != nil {
			stats.RemainingCandidates = remainingCandidateEstimate(stats)
			return stats, fmt.Errorf("close raw usage evidence batch: %w", err)
		}

		if len(candidates) > 0 {
			if err := d.applyRawEvidenceBatch(candidates); err != nil {
				stats.RemainingCandidates = remainingCandidateEstimate(stats)
				return stats, err
			}
			stats.Updated += int64(len(candidates))
			stats.BatchesCompleted++
		}
		if rowCount < rawEvidenceBatchSize {
			break
		}
	}
	remaining, err := d.InspectRawEvidence()
	if err != nil {
		stats.RemainingCandidates = remainingCandidateEstimate(stats)
		return stats, err
	}
	stats.RawBytesAfter = remaining.RawBytesBefore
	stats.RemainingCandidates = remaining.Candidates
	if stats.RemainingCandidates != 0 {
		return stats, errors.New("raw usage evidence compaction incomplete; retry")
	}
	return stats, nil
}

func remainingCandidateEstimate(stats RawEvidenceStats) int64 {
	remaining := stats.Candidates - stats.Updated
	if remaining < 0 {
		return 0
	}
	return remaining
}

func rawEvidenceCandidateForRow(eventID, channel, strategy, raw string) (rawEvidenceCandidate, error) {
	if rawEvidenceIdentityProtected(strategy) {
		return rawEvidenceCandidate{}, nil
	}
	result, err := compactEvidence(channel, raw)
	if err != nil {
		return rawEvidenceCandidate{}, err
	}
	if result.Status != usageevidence.StatusRecognizedLegacy {
		return rawEvidenceCandidate{}, nil
	}
	return rawEvidenceCandidate{eventID: eventID, raw: raw, compact: result.JSON}, nil
}

func (d *Database) applyRawEvidenceBatch(candidates []rawEvidenceCandidate) (err error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("start raw usage evidence batch: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, candidate := range candidates {
		result, err := tx.Exec(`
			UPDATE usage_events
			SET raw_usage_json = ?
			WHERE event_id = ? AND raw_usage_json = ?
		`, candidate.compact, candidate.eventID, candidate.raw)
		if err != nil {
			return fmt.Errorf("compact raw usage evidence batch: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("verify raw usage evidence batch: %w", err)
		}
		if rowsAffected != 1 {
			return errors.New("raw usage evidence changed concurrently; retry")
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit raw usage evidence batch: %w", err)
	}
	return nil
}

func compactStoredCodexEvidence(incoming *model.UsageEvent, stored []*model.UsageEvent) string {
	if incoming == nil || incoming.Channel != "codex" {
		return ""
	}
	for _, candidate := range stored {
		if candidate != nil && usageevidence.IsCompact("codex", candidate.RawUsageJSON) {
			return candidate.RawUsageJSON
		}
	}
	return ""
}

func storedIdentityProtection(exact *model.UsageEvent, stored []*model.UsageEvent) *model.UsageEvent {
	if exact != nil && rawEvidenceIdentityProtected(exact.DedupeStrategy) {
		return exact
	}
	for _, candidate := range stored {
		if candidate != nil && rawEvidenceIdentityProtected(candidate.DedupeStrategy) {
			return candidate
		}
	}
	return nil
}

func compactCodexEvidence(raw string) bool {
	return usageevidence.IsCompact("codex", raw)
}
