package adapters

import (
	"encoding/json"
	"sort"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type codexCompaction struct {
	record    *fingerprint.ParsedRecord
	usage     codexUsageSnapshot
	thread    codexUsageSnapshot
	hasThread bool
	matched   bool
	covered   bool
}

type codexCompactions struct {
	byID   map[string]*codexCompaction
	latest map[string]string
	order  []string
}

func newCodexCompactions() *codexCompactions {
	return &codexCompactions{byID: make(map[string]*codexCompaction), latest: make(map[string]string)}
}

func (c *codexCompactions) observe(obj map[string]interface{}, path string, line int, session, sessionPath string, state codexModelState, tier, policy string) {
	payload := getMap(obj, "payload")
	id := getString(payload, "response_id")
	marker := getString(obj, "type") == "compacted"
	if marker {
		id = getString(payload, "compaction_response_id")
	}
	if id == "" {
		return
	}
	entry := c.byID[id]
	if entry == nil {
		entry = &codexCompaction{}
		c.byID[id] = entry
		c.order = append(c.order, id)
	}
	if marker {
		entry.matched = true
		return
	}
	if entry.record != nil {
		return
	}
	raw := getMap(payload, "usage")
	if raw == nil {
		return
	}
	usage := codexUsageFromMap(raw)
	if usage.isZero() || usage.Input < 0 || usage.Output < 0 || usage.CachedInput < 0 || usage.Reasoning < 0 || usage.Total < 0 {
		return
	}
	if !usage.HasTotal {
		usage.Total, usage.HasTotal = usage.Input+maxInt64(usage.Output, usage.Reasoning), true
	}
	normalized, partial := usage.reconcileToSourceTotal()
	stored := normalized.storageUsage()
	name := extractCodexModel(obj)
	resolution := model.ModelResolutionDirectEvent
	if name == "" {
		name, resolution = state.model, state.resolution
	}
	if name == "" {
		name, resolution = "unknown", model.ModelResolutionUnknown
	}
	if recorded := getString(payload, "service_tier"); recorded != "" {
		tier = model.NormalizeServiceTier(recorded)
	}
	session = extractCodexSessionID(obj, session)
	rawJSON, _ := json.Marshal(obj)
	observability := "full"
	if partial {
		observability = "partial"
	}
	entry.record = &fingerprint.ParsedRecord{
		Agent: "codex", Provider: "openai", SourceProduct: "codex-cli", Model: name, ModelResolution: resolution,
		ModelIsFallback: name == "unknown", NativeSessionID: extractCodexExplicitSessionID(obj), SessionID: session, SessionPathID: sessionPath,
		NativeEventID: id, RequestID: id, IdentityKind: "request", IdentityScope: "global", IdentitySubkey: "compaction",
		ParserVersion: "codex-v2", Granularity: "request", TimestampMs: extractCodexTimestamp(obj),
		InputTokens: stored.Input, OutputTokens: stored.Output, CacheReadTokens: stored.CachedInput, ReasoningTokens: stored.Reasoning,
		TotalTokens: stored.Total, SourceTotalTokens: usage.sourceTotalPtr(), RawInputTokens: normalized.inputPtr(),
		TokenAccountingMethod: model.AccCodexHeadlessUsage, AccountingProfile: model.AccountingProfileWithTier(policy, tier), UsageSpeed: tier,
		ObservabilityLevel: observability, SourceFile: path, LineNumber: line, FingerprintJSON: string(rawJSON), RawSHA256: sha256Hex(rawJSON),
	}
	entry.usage = usage
	if thread := getMap(payload, "thread_token_usage"); thread != nil {
		entry.thread, entry.hasThread = codexUsageFromMap(thread), true
	}
	c.latest[session] = id
}

func (c *codexCompactions) observeCumulative(total, delta codexUsageSnapshot, session string) {
	entry := c.byID[c.latest[session]]
	if entry == nil || entry.record == nil || entry.covered || entry.matched || delta.isZero() {
		return
	}
	delete(c.latest, session)
	if codexUsageSnapshotEqual(entry.usage, delta) || entry.hasThread && codexUsageSnapshotEqual(entry.thread, total) {
		entry.covered = true
	}
}

func (c *codexCompactions) records() []*fingerprint.ParsedRecord {
	var result []*fingerprint.ParsedRecord
	for _, id := range c.order {
		entry := c.byID[id]
		if !entry.matched || entry.record == nil {
			continue
		}
		if entry.covered {
			entry.record.UsageSegment = "covered_compaction"
		}
		result = append(result, entry.record)
	}
	return result
}

func (a *CodexAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	covered := make(map[string]bool)
	for _, rec := range records {
		if rec.UsageSegment == "covered_compaction" {
			covered[rec.NativeEventID] = true
		}
	}
	// Parent copies win deterministically even when child files sort earlier.
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if (left.IdentitySubkey == "compaction") != (right.IdentitySubkey == "compaction") {
			return left.IdentitySubkey != "compaction"
		}
		if left.IdentitySubkey != "compaction" {
			return false
		}
		leftChild, rightChild := false, false
		if a.replayPlan != nil {
			_, leftChild = a.replayPlan.byChild[left.SourceFile]
			_, rightChild = a.replayPlan.byChild[right.SourceFile]
		}
		return !leftChild && rightChild
	})
	seen := make(map[string]bool)
	result := make([]*fingerprint.ParsedRecord, 0, len(records))
	for _, rec := range records {
		if rec.IdentitySubkey == "compaction" {
			if covered[rec.NativeEventID] || seen[rec.NativeEventID] {
				continue
			}
			seen[rec.NativeEventID] = true
		}
		result = append(result, rec)
	}
	return result
}
