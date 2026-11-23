package db

import (
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const antigravityPreviewParser = "antigravity-v1"

func isAntigravityEvent(event *model.UsageEvent) bool {
	return event != nil && event.Channel == model.AntigravitySource &&
		event.SourceProduct == model.AntigravitySource && event.TokenAccountingMethod == model.AccAntigravityModelUsage &&
		(event.ParserVersion == model.AntigravityParser || event.ParserVersion == antigravityPreviewParser)
}

func antigravityIdentity(event *model.UsageEvent) model.AntigravityRequestIdentity {
	var ids model.AntigravityRequestIdentity
	if event.ParserVersion == model.AntigravityParser {
		if err := json.Unmarshal([]byte(event.RequestID), &ids); err != nil {
			return model.AntigravityRequestIdentity{}
		}
	}
	return ids
}

func validateAntigravityEvidence(event *model.UsageEvent) error {
	if event.ParserVersion != model.AntigravityParser {
		return nil
	}
	if !isAntigravityEvent(event) || event.EventGranularity != "request" {
		return reject("invalid_antigravity_source")
	}
	if _, ok := model.ParseAntigravityAccountingEvidence(event.AccountingProfile); !ok {
		return reject("invalid_antigravity_accounting_evidence")
	}
	ids := antigravityIdentity(event)
	keys := slices.Concat(ids.Native, ids.Rows, ids.Legacy)
	if len(ids.Rows) == 0 || !slices.Contains(keys, event.EventID) {
		return reject("invalid_antigravity_identity_evidence")
	}
	for _, key := range keys {
		if !isCanonicalSHA256(key) {
			return reject("invalid_antigravity_identity_evidence")
		}
	}
	return nil
}

func antigravityKeys(event *model.UsageEvent) []string {
	ids := antigravityIdentity(event)
	return slices.Concat([]string{event.EventID}, ids.Native, ids.Rows, ids.Legacy)
}

// antigravityLink checks an actual alias intersection. Row keys can promote
// an unkeyed observation but cannot join two distinct native requests.
func antigravityLink(left, right *model.UsageEvent) (linked, native bool) {
	if !isAntigravityEvent(left) || !isAntigravityEvent(right) {
		return false, false
	}
	if left.ParserVersion == antigravityPreviewParser {
		if right.ParserVersion == antigravityPreviewParser {
			return left.EventID == right.EventID, left.IdentityScope == "global"
		}
		return antigravityPreviewLink(left, right)
	}
	if right.ParserVersion == antigravityPreviewParser {
		return antigravityPreviewLink(right, left)
	}
	a, b := antigravityIdentity(left), antigravityIdentity(right)
	if antigravitySharesKey(a.Native, b.Native) {
		return true, true
	}
	return (len(a.Native) == 0 || len(b.Native) == 0) &&
		antigravitySharesKey(a.Rows, b.Rows), false
}

func antigravityPreviewLink(preview, current *model.UsageEvent) (linked, native bool) {
	ids := antigravityIdentity(current)
	switch {
	case preview.IdentityScope == "global" && preview.IdentityStrategy == "native_message":
		match := preview.MessageID != "" && preview.EventID == fingerprint.AntigravityNativeEventID("", preview.MessageID) &&
			slices.Contains(ids.Legacy, preview.EventID)
		return match, match
	case preview.IdentityStrategy == "session_record":
		return slices.Contains(ids.Rows, preview.EventID) || slices.Contains(ids.Legacy, preview.EventID), false
	default:
		return false, false
	}
}

func antigravitySharesKey(left, right []string) bool {
	for _, key := range left {
		if slices.Contains(right, key) {
			return true
		}
	}
	return false
}

func selectAntigravityMatch(tx *sql.Tx, incoming, found *model.UsageEvent) (*model.UsageEvent, error) {
	// Reimports of an unchanged row are the common path. New or changed alias
	// sets need a transactional lookup, including previously imported copies.
	if found != nil && isAntigravityEvent(found) && found.RequestID == incoming.RequestID {
		return found, nil
	}
	keysJSON, _ := json.Marshal(antigravityKeys(incoming))
	rows, err := tx.Query(`WITH keys AS MATERIALIZED (
			SELECT value, '"' || value || '"' AS quoted FROM json_each(?)
		)
		SELECT `+eventColumns+` FROM usage_events
		WHERE source_product = 'antigravity' AND channel = 'antigravity'
		AND EXISTS (SELECT 1 FROM keys
			WHERE usage_events.event_id = keys.value OR instr(request_id, keys.quoted) > 0)`, string(keysJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		candidate, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		if linked, _ := antigravityLink(candidate, incoming); !linked {
			continue
		}
		if found != nil && found.EventID != candidate.EventID {
			return nil, reject("antigravity_identity_ambiguous")
		}
		found = candidate
	}
	return found, rows.Err()
}

// The merge preflight works with a virtual ledger, so newly accepted aliases
// must be immediately visible to later rows in the source export.
type antigravityMergeIndex map[string]map[string]struct{}

func (index antigravityMergeIndex) add(event *model.UsageEvent) {
	if !isAntigravityEvent(event) {
		return
	}
	for _, key := range antigravityKeys(event) {
		if index[key] == nil {
			index[key] = make(map[string]struct{})
		}
		index[key][event.EventID] = struct{}{}
	}
}

func (index antigravityMergeIndex) remove(event *model.UsageEvent) {
	if !isAntigravityEvent(event) {
		return
	}
	for _, key := range antigravityKeys(event) {
		delete(index[key], event.EventID)
		if len(index[key]) == 0 {
			delete(index, key)
		}
	}
}

func (index antigravityMergeIndex) match(virtual map[string]*model.UsageEvent, incoming, found *model.UsageEvent) (*model.UsageEvent, error) {
	for _, key := range antigravityKeys(incoming) {
		for id := range index[key] {
			candidate := virtual[id]
			if linked, _ := antigravityLink(candidate, incoming); !linked {
				continue
			}
			if found != nil && found.EventID != candidate.EventID {
				return nil, reject("antigravity_identity_ambiguous")
			}
			found = candidate
		}
	}
	return found, nil
}

func reconcileAntigravity(existing, incoming *model.UsageEvent) (*model.UsageEvent, string, error) {
	linked, native := antigravityLink(existing, incoming)
	if !linked {
		return nil, ReconcileRejected, reject("antigravity_identity_conflict")
	}
	for _, event := range []*model.UsageEvent{existing, incoming} {
		if event.ParserVersion == antigravityPreviewParser && event.IdentityStrategy == "session_record" &&
			event.EventID == fingerprint.AntigravityRecordEventID(event.SessionID, "record") {
			return nil, ReconcileRejected, reject("antigravity_preview_retry_ambiguous")
		}
	}
	if !native && !sameAntigravityUsage(existing, incoming) {
		return nil, ReconcileRejected, reject("antigravity_row_conflict")
	}
	if incoming.ParserVersion == antigravityPreviewParser {
		return existing, ReconcileSkipped, nil
	}
	canonical := *existing
	if existing.ParserVersion == antigravityPreviewParser {
		// The v1 preview did not retain timestamp provenance or output split
		// evidence. A proven match lets the v2 observation replace those facts.
		canonical = *incoming
		canonical.EventID, canonical.IdentityScope, canonical.IdentityStrategy = existing.EventID, existing.IdentityScope, existing.IdentityStrategy
		canonical.SessionKey, canonical.SessionID, canonical.SessionPathID = existing.SessionKey, existing.SessionID, existing.SessionPathID
	} else {
		currentEvidence, _ := model.ParseAntigravityAccountingEvidence(existing.AccountingProfile)
		incomingEvidence, _ := model.ParseAntigravityAccountingEvidence(incoming.AccountingProfile)
		if incomingEvidence.Time == currentEvidence.Time && incoming.TimestampMs != existing.TimestampMs &&
			currentEvidence.Time != model.AntigravityTrajectoryTime {
			return nil, ReconcileRejected, reject("timestamp_conflict")
		}
		if incomingEvidence.Time > currentEvidence.Time ||
			(incomingEvidence.Time == currentEvidence.Time && incoming.TimestampMs < canonical.TimestampMs) {
			canonical.TimestampMs, currentEvidence.Time = incoming.TimestampMs, incomingEvidence.Time
		}
		currentOutput := model.AntigravityOutput{Visible: canonical.OutputTokens, Reasoning: canonical.ReasoningTokens, Evidence: currentEvidence.Output}
		incomingOutput := model.AntigravityOutput{Visible: incoming.OutputTokens, Reasoning: incoming.ReasoningTokens, Evidence: incomingEvidence.Output}
		if incomingOutput.BetterThan(currentOutput) {
			canonical.OutputTokens, canonical.ReasoningTokens = incomingOutput.Visible, incomingOutput.Reasoning
			currentEvidence.Output = incomingEvidence.Output
		}
		canonical.InputTokens = max(canonical.InputTokens, incoming.InputTokens)
		canonical.CacheCreationTokens = max(canonical.CacheCreationTokens, incoming.CacheCreationTokens)
		canonical.CacheReadTokens = max(canonical.CacheReadTokens, incoming.CacheReadTokens)
		total, ok := model.AntigravityTotal(canonical.InputTokens, canonical.OutputTokens, canonical.ReasoningTokens, canonical.CacheCreationTokens, canonical.CacheReadTokens)
		if !ok {
			return nil, ReconcileRejected, reject("accounting_total_mismatch")
		}
		canonical.TotalTokens = total
		canonical.AccountingProfile = currentEvidence.Profile()

		currentRank, incomingRank := modelEvidenceRank(existing), modelEvidenceRank(incoming)
		if currentRank == 2 && incomingRank == 2 && knownConflict(existing.ModelNormalized, incoming.ModelNormalized) {
			return nil, ReconcileRejected, reject("direct_model_conflict")
		}
		if incomingRank > currentRank {
			canonical.ModelNormalized, canonical.ModelRaw = incoming.ModelNormalized, incoming.ModelRaw
			canonical.ModelResolution, canonical.ModelIsFallback = incoming.ModelResolution, incoming.ModelIsFallback
			canonical.Provider = incoming.Provider
		} else if !knownConflict(canonical.ModelNormalized, incoming.ModelNormalized) {
			fillKnownString(&canonical.Provider, incoming.Provider)
		}
	}

	a, b := antigravityIdentity(existing), antigravityIdentity(incoming)
	ids := model.AntigravityRequestIdentity{
		Native: mergeAntigravityKeys(a.Native, b.Native),
		Rows:   mergeAntigravityKeys(a.Rows, b.Rows),
		Legacy: mergeAntigravityKeys(a.Legacy, b.Legacy),
	}
	evidence, _ := json.Marshal(ids)
	canonical.RequestID = string(evidence)
	fillString(&canonical.MessageID, incoming.MessageID)
	fillString(&canonical.SourceFile, incoming.SourceFile)
	fillString(&canonical.RawSHA256, incoming.RawSHA256)
	if canonical.LineNumber == 0 {
		canonical.LineNumber = incoming.LineNumber
	}
	canonical.ImportedAtMs, canonical.UpdatedAtMs = existing.ImportedAtMs, existing.UpdatedAtMs
	hash, err := contentSHA256ForEvent(&canonical)
	if err != nil {
		return nil, ReconcileRejected, reject("invalid_content_hash")
	}
	canonical.ContentSHA256 = hash
	if canonical == *existing {
		return existing, ReconcileSkipped, nil
	}
	canonical.UpdatedAtMs = max(incoming.UpdatedAtMs, time.Now().UnixMilli())
	return &canonical, ReconcileUpdated, nil
}

func sameAntigravityUsage(left, right *model.UsageEvent) bool {
	return left.InputTokens == right.InputTokens && left.CacheCreationTokens == right.CacheCreationTokens &&
		left.CacheReadTokens == right.CacheReadTokens && left.TotalTokens == right.TotalTokens
}

func mergeAntigravityKeys(left, right []string) []string {
	keys := slices.Concat(left, right)
	slices.Sort(keys)
	return slices.Compact(keys)
}
