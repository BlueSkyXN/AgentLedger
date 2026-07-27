package db

import (
	"errors"
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const rawEvidenceBatchSize = 1000

// RawEvidenceStats is aggregate-only. It never returns envelopes, event IDs,
// source locations, or any other protected usage detail.
type RawEvidenceStats struct {
	// Candidates counts raw_usage_json IS NOT NULL, including empty strings.
	Candidates int64
	// AlreadyNull counts rows that already comply with the storage boundary.
	AlreadyNull         int64
	RawBytesBefore      int64
	RawBytesAfter       int64
	Updated             int64
	BatchesCompleted    int64
	RemainingCandidates int64
}

type rawEvidenceCandidate struct {
	rowID   int64
	eventID string
	raw     string
}

// InspectRawEvidence computes the no-write cleanup plan. It is safe through
// OpenReadOnlyV2 and considers every non-NULL value stale, regardless of its
// channel, dedupe strategy, or evidence format.
func (d *Database) InspectRawEvidence() (RawEvidenceStats, error) {
	var stats RawEvidenceStats
	if err := d.conn.QueryRow(`
		SELECT
			COUNT(CASE WHEN raw_usage_json IS NOT NULL THEN 1 END),
			COUNT(CASE WHEN raw_usage_json IS NULL THEN 1 END),
			COALESCE(SUM(CASE WHEN raw_usage_json IS NOT NULL THEN length(CAST(raw_usage_json AS BLOB)) ELSE 0 END), 0)
		FROM usage_events
	`).Scan(&stats.Candidates, &stats.AlreadyNull, &stats.RawBytesBefore); err != nil {
		return stats, fmt.Errorf("inspect raw usage evidence: %w", err)
	}
	// Apply writes NULL, whose logical payload occupies no raw bytes.
	stats.RawBytesAfter = 0
	stats.RemainingCandidates = stats.Candidates
	return stats, nil
}

// CompactRawEvidence retains its public name for CLI compatibility. It now
// removes every non-NULL raw envelope in atomic keyset batches instead of
// transforming selected evidence formats.
func (d *Database) CompactRawEvidence() (RawEvidenceStats, error) {
	stats, err := d.InspectRawEvidence()
	if err != nil {
		return stats, err
	}
	if stats.Candidates == 0 {
		return stats, nil
	}
	if _, err := d.conn.Exec(`PRAGMA secure_delete = ON`); err != nil {
		return stats, fmt.Errorf("enable secure delete: %w", err)
	}

	var lastRowID int64
	for {
		rows, err := d.conn.Query(`
			SELECT rowid, event_id, raw_usage_json
			FROM usage_events
			WHERE rowid > ? AND raw_usage_json IS NOT NULL
			ORDER BY rowid
			LIMIT ?
		`, lastRowID, rawEvidenceBatchSize)
		if err != nil {
			stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
			return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
		}

		var candidates []rawEvidenceCandidate
		for rows.Next() {
			candidate := rawEvidenceCandidate{}
			if err := rows.Scan(&candidate.rowID, &candidate.eventID, &candidate.raw); err != nil {
				_ = rows.Close()
				stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
				return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
			}
			lastRowID = candidate.rowID
			candidates = append(candidates, candidate)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
			return stats, fmt.Errorf("read raw usage evidence batch: %w", err)
		}
		if err := rows.Close(); err != nil {
			stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
			return stats, fmt.Errorf("close raw usage evidence batch: %w", err)
		}
		if len(candidates) == 0 {
			break
		}
		if err := d.applyRawEvidenceBatch(candidates); err != nil {
			stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
			return stats, err
		}
		stats.Updated += int64(len(candidates))
		stats.BatchesCompleted++
		if len(candidates) < rawEvidenceBatchSize {
			break
		}
	}

	remaining, err := d.InspectRawEvidence()
	if err != nil {
		stats.RemainingCandidates = remainingRawEvidenceCandidates(stats)
		return stats, err
	}
	stats.RawBytesAfter = remaining.RawBytesBefore
	stats.RemainingCandidates = remaining.Candidates
	if stats.RemainingCandidates != 0 {
		return stats, errors.New("raw usage evidence cleanup incomplete; retry")
	}
	return stats, nil
}

func remainingRawEvidenceCandidates(stats RawEvidenceStats) int64 {
	remaining := stats.Candidates - stats.Updated
	if remaining < 0 {
		return 0
	}
	return remaining
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
			SET raw_usage_json = NULL
			WHERE event_id = ? AND raw_usage_json = ?
		`, candidate.eventID, candidate.raw)
		if err != nil {
			return fmt.Errorf("clear raw usage evidence batch: %w", err)
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

func rawEvidenceIdentityProtected(strategy string) bool {
	return strategy == "raw_hash" || strategy == "fallback"
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
