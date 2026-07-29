package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const codexReplayBurstPauseMs int64 = 1000

const (
	codexDiagnosticForkFiles         = "codex_fork_files"
	codexDiagnosticParentResolved    = "codex_parent_resolved"
	codexDiagnosticParentMissing     = "codex_parent_missing"
	codexDiagnosticParentAmbiguous   = "codex_parent_ambiguous"
	codexDiagnosticReplayExact       = "codex_replay_exact"
	codexDiagnosticReplayRewritten   = "codex_replay_rewritten"
	codexDiagnosticReplayEvents      = "codex_replay_events"
	codexDiagnosticReplayTokens      = "codex_replay_tokens"
	codexDiagnosticReplayUnresolved  = "codex_replay_unresolved"
	codexDiagnosticReplayFileChanged = "codex_replay_file_changed"
	codexDiagnosticReplayPlanFailed  = "codex_replay_plan_failed"
)

type codexReplayPhase uint8

const (
	codexReplayMatchingParent codexReplayPhase = iota
	codexReplaySkippingBurst
	codexReplayDone
)

type codexReplayUsage struct {
	TimestampMs int64
	Input       int64
	Cached      int64
	Output      int64
	Reasoning   int64
	Total       int64
}

type codexFileIdentity struct {
	size      int64
	modTimeNS int64
}

type codexSessionMetadata struct {
	path       string
	sessionID  string
	parentID   string
	forkedAtMs int64
	identity   codexFileIdentity
}

type codexChildReplayPlan struct {
	parentPath     string
	parentPrefix   []codexReplayUsage
	burstStartMs   int64
	childIdentity  codexFileIdentity
	parentIdentity codexFileIdentity
	quarantine     string
}

type codexReplayPlan struct {
	byChild map[string]codexChildReplayPlan
}

type codexReplayMatcher struct {
	phase                codexReplayPhase
	parentPrefix         []codexReplayUsage
	prefixIndex          int
	burstStartMs         int64
	previousBurstMs      int64
	replaySkipped        bool
	replayTimestampValid bool
	lastReplayTimestamp  int64
}

type codexReplayDecision struct {
	skip         bool
	transitioned bool
	method       string
}

type codexReplayStats struct {
	forkFiles       int64
	parentResolved  int64
	parentMissing   int64
	parentAmbiguous int64
	exactEvents     int64
	exactTokens     int64
	rewrittenEvents int64
	rewrittenTokens int64
	unresolved      int64
	fileChanged     int64
	planFailed      int64
}

type codexReplayQuarantineError struct {
	reason string
}

func (e *codexReplayQuarantineError) Error() string {
	return "codex replay source quarantined: " + e.reason
}

func (e *codexReplayQuarantineError) Quarantined() bool { return true }

func (a *CodexAdapter) PrepareFileSet(paths []string) error {
	plan, stats, err := buildCodexReplayPlan(paths)
	if err != nil {
		stats.planFailed++
		a.replayPlan = nil
		a.replayStats = stats
		return err
	}
	a.replayPlan = plan
	a.replayStats = stats
	return nil
}

func buildCodexReplayPlan(paths []string) (*codexReplayPlan, codexReplayStats, error) {
	replayPlan := &codexReplayPlan{byChild: map[string]codexChildReplayPlan{}}
	stats := codexReplayStats{}
	ordered := make([]string, 0, len(paths))
	seenPaths := map[string]bool{}
	for _, path := range paths {
		canonical, err := canonicalCodexPath(path)
		if err != nil {
			return nil, stats, err
		}
		if !seenPaths[canonical] {
			seenPaths[canonical] = true
			ordered = append(ordered, canonical)
		}
	}
	sort.Strings(ordered)
	metadata := make([]codexSessionMetadata, 0, len(ordered))
	bySessionID := map[string][]codexSessionMetadata{}
	for _, path := range ordered {
		item, err := readCodexSessionMetadata(path)
		if err != nil {
			return nil, stats, err
		}
		metadata = append(metadata, item)
		if item.sessionID != "" {
			bySessionID[item.sessionID] = append(bySessionID[item.sessionID], item)
		}
	}

	parentUsage := map[string][]codexReplayUsage{}
	for _, child := range metadata {
		if child.parentID == "" {
			continue
		}
		stats.forkFiles++
		childPlan := codexChildReplayPlan{childIdentity: child.identity}

		candidates := append([]codexSessionMetadata(nil), bySessionID[child.parentID]...)
		parents, status := resolveCodexReplayParents(child, candidates)
		var parent codexSessionMetadata
		var parentPrefix []codexReplayUsage
		exactReplayUnavailable := ""
		if status == "resolved" {
			parent = parents[0]
			if child.forkedAtMs <= 0 {
				exactReplayUnavailable = "fork timestamp is unavailable"
			} else {
				stream, err := loadCodexReplayUsage(parentUsage, parent.path)
				if err != nil {
					return nil, stats, fmt.Errorf("read Codex replay parent: %w", err)
				}
				var ok bool
				parentPrefix, ok = codexReplayPrefixAt(stream, child.forkedAtMs)
				if !ok {
					exactReplayUnavailable = "parent replay usage timestamp is unavailable before the fork boundary"
				}
			}
			if len(parents) > 1 {
				if exactReplayUnavailable != "" {
					// Multiple same-rank parents are safe only when their bounded
					// replay prefixes can be proven equivalent. A missing boundary
					// must not let an opening burst bypass parent ambiguity.
					status = "ambiguous"
				} else {
					for _, candidate := range parents[1:] {
						stream, err := loadCodexReplayUsage(parentUsage, candidate.path)
						if err != nil {
							return nil, stats, fmt.Errorf("read Codex replay parent: %w", err)
						}
						prefix, ok := codexReplayPrefixAt(stream, child.forkedAtMs)
						if !ok || !codexReplayUsageSlicesEqual(parentPrefix, prefix) {
							status = "ambiguous"
							break
						}
					}
				}
			}
		}
		switch status {
		case "resolved":
			stats.parentResolved++
			childPlan.parentPath = parent.path
			childPlan.parentIdentity = parent.identity
			if exactReplayUnavailable == "" {
				childPlan.parentPrefix = parentPrefix
			}
		case "missing":
			stats.parentMissing++
		case "ambiguous":
			stats.parentAmbiguous++
			stats.unresolved++
			childPlan.quarantine = "ambiguous parent session"
		case "self":
			stats.parentAmbiguous++
			stats.unresolved++
			childPlan.quarantine = "parent session references the child itself"
		}

		provenEmptyParentPrefix := status == "resolved" && exactReplayUnavailable == "" && len(parentPrefix) == 0
		if childPlan.quarantine == "" && !provenEmptyParentPrefix {
			burstStart, err := detectCodexRewrittenBurst(child.path)
			if err != nil {
				return nil, stats, fmt.Errorf("inspect Codex replay child: %w", err)
			}
			childPlan.burstStartMs = burstStart
			if status == "missing" && burstStart == 0 {
				stats.unresolved++
				childPlan.quarantine = "parent session is unavailable and no rewritten burst is provable"
			} else if exactReplayUnavailable != "" && burstStart == 0 {
				stats.unresolved++
				childPlan.quarantine = exactReplayUnavailable + " and no rewritten burst is provable"
			}
		}
		replayPlan.byChild[child.path] = childPlan
	}
	return replayPlan, stats, nil
}

func (a *CodexAdapter) ImportDiagnostics() []ImportDiagnostic {
	stats := a.replayStats
	return []ImportDiagnostic{
		{Code: codexDiagnosticForkFiles, Unit: ImportDiagnosticUnitCount, Count: stats.forkFiles},
		{Code: codexDiagnosticParentResolved, Unit: ImportDiagnosticUnitCount, Count: stats.parentResolved},
		{Code: codexDiagnosticParentMissing, Unit: ImportDiagnosticUnitCount, Count: stats.parentMissing},
		{Code: codexDiagnosticParentAmbiguous, Unit: ImportDiagnosticUnitCount, Count: stats.parentAmbiguous},
		{Code: codexDiagnosticReplayExact, Unit: ImportDiagnosticUnitUsage, Events: stats.exactEvents, Tokens: stats.exactTokens},
		{Code: codexDiagnosticReplayRewritten, Unit: ImportDiagnosticUnitUsage, Events: stats.rewrittenEvents, Tokens: stats.rewrittenTokens},
		{Code: codexDiagnosticReplayEvents, Unit: ImportDiagnosticUnitEvents, Events: stats.exactEvents + stats.rewrittenEvents},
		{Code: codexDiagnosticReplayTokens, Unit: ImportDiagnosticUnitTokens, Tokens: stats.exactTokens + stats.rewrittenTokens},
		{Code: codexDiagnosticReplayUnresolved, Unit: ImportDiagnosticUnitCount, Count: stats.unresolved},
		{Code: codexDiagnosticReplayFileChanged, Unit: ImportDiagnosticUnitCount, Count: stats.fileChanged},
		{Code: codexDiagnosticReplayPlanFailed, Unit: ImportDiagnosticUnitCount, Count: stats.planFailed},
	}
}

func (a *CodexAdapter) replayMatcherFor(path string) (*codexReplayMatcher, error) {
	if a.replayPlan == nil {
		return nil, nil
	}
	canonical, err := canonicalCodexPath(path)
	if err != nil {
		return nil, err
	}
	plan, ok := a.replayPlan.byChild[canonical]
	if !ok {
		return nil, nil
	}
	if plan.quarantine != "" {
		return nil, &codexReplayQuarantineError{reason: plan.quarantine}
	}
	identity, err := codexIdentity(canonical)
	if err != nil {
		a.replayStats.fileChanged++
		return nil, &codexReplayQuarantineError{reason: "child file became unavailable after replay preparation"}
	}
	if identity != plan.childIdentity {
		a.replayStats.fileChanged++
		return nil, &codexReplayQuarantineError{reason: "child file changed after replay preparation"}
	}
	if plan.parentPath != "" {
		identity, err = codexIdentity(plan.parentPath)
		if err != nil {
			a.replayStats.fileChanged++
			return nil, &codexReplayQuarantineError{reason: "parent file became unavailable after replay preparation"}
		}
		if identity != plan.parentIdentity {
			a.replayStats.fileChanged++
			return nil, &codexReplayQuarantineError{reason: "parent file changed after replay preparation"}
		}
	}
	return &codexReplayMatcher{
		phase:        codexReplayMatchingParent,
		parentPrefix: plan.parentPrefix,
		burstStartMs: plan.burstStartMs,
	}, nil
}

func (m *codexReplayMatcher) pending() bool {
	return m != nil && m.phase != codexReplayDone
}

func (m *codexReplayMatcher) observe(current codexReplayUsage) (codexReplayDecision, error) {
	if m == nil || m.phase == codexReplayDone {
		return codexReplayDecision{}, nil
	}
	for {
		switch m.phase {
		case codexReplayMatchingParent:
			if m.prefixIndex < len(m.parentPrefix) && codexReplayUsageEqual(m.parentPrefix[m.prefixIndex], current) {
				m.prefixIndex++
				m.recordReplayTimestamp(current.TimestampMs)
				return codexReplayDecision{skip: true, method: "exact"}, nil
			}
			if m.prefixIndex > 0 {
				m.phase = codexReplayDone
				return codexReplayDecision{transitioned: true}, nil
			}
			if m.burstStartMs == 0 {
				// A uniquely resolved parent plus an opening mismatch is affirmative
				// evidence that this child started with its own usage. Missing parents
				// without burst evidence were already quarantined during preparation.
				m.phase = codexReplayDone
				return codexReplayDecision{transitioned: true}, nil
			}
			m.phase = codexReplaySkippingBurst
			m.previousBurstMs = m.burstStartMs
		case codexReplaySkippingBurst:
			delta := current.TimestampMs - m.previousBurstMs
			if current.TimestampMs > 0 && m.previousBurstMs > 0 && delta >= 0 && delta <= codexReplayBurstPauseMs {
				m.previousBurstMs = current.TimestampMs
				m.recordReplayTimestamp(current.TimestampMs)
				return codexReplayDecision{skip: true, method: "rewritten"}, nil
			}
			m.phase = codexReplayDone
			return codexReplayDecision{transitioned: true}, nil
		default:
			return codexReplayDecision{}, nil
		}
	}
}

func (m *codexReplayMatcher) recordReplayTimestamp(timestamp int64) {
	if !m.replaySkipped {
		m.replaySkipped = true
		m.replayTimestampValid = timestamp > 0
		m.lastReplayTimestamp = timestamp
		return
	}
	if !m.replayTimestampValid || timestamp <= 0 || timestamp < m.lastReplayTimestamp {
		m.replayTimestampValid = false
		return
	}
	m.lastReplayTimestamp = timestamp
}

func (a *CodexAdapter) recordReplaySkip(method string, tokens int64) {
	switch method {
	case "exact":
		a.replayStats.exactEvents++
		a.replayStats.exactTokens += tokens
	case "rewritten":
		a.replayStats.rewrittenEvents++
		a.replayStats.rewrittenTokens += tokens
	}
}

func resolveCodexReplayParents(child codexSessionMetadata, candidates []codexSessionMetadata) ([]codexSessionMetadata, string) {
	filtered := make([]codexSessionMetadata, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.path != child.path && candidate.sessionID != child.sessionID {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		for _, candidate := range candidates {
			if candidate.path == child.path || candidate.sessionID == child.sessionID {
				return nil, "self"
			}
		}
		return nil, "missing"
	}
	sort.Slice(filtered, func(i, j int) bool {
		leftRank, rightRank := codexSessionPathRank(filtered[i].path), codexSessionPathRank(filtered[j].path)
		if leftRank == rightRank {
			return filtered[i].path < filtered[j].path
		}
		return leftRank < rightRank
	})
	bestRank := codexSessionPathRank(filtered[0].path)
	limit := 1
	for limit < len(filtered) && codexSessionPathRank(filtered[limit].path) == bestRank {
		limit++
	}
	return filtered[:limit], "resolved"
}

func canonicalCodexPath(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
}

func loadCodexReplayUsage(cache map[string][]codexReplayUsage, path string) ([]codexReplayUsage, error) {
	if stream, ok := cache[path]; ok {
		return stream, nil
	}
	stream, err := readCodexReplayUsage(path)
	if err != nil {
		return nil, err
	}
	cache[path] = stream
	return stream, nil
}

func codexReplayUsageSlicesEqual(left, right []codexReplayUsage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !codexReplayUsageEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func readCodexSessionMetadata(path string) (codexSessionMetadata, error) {
	identity, err := codexIdentity(path)
	if err != nil {
		return codexSessionMetadata{}, err
	}
	item := codexSessionMetadata{path: path, identity: identity}
	f, err := os.Open(path)
	if err != nil {
		return codexSessionMetadata{}, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, codexScannerInitialBufferBytes), codexScannerMaxTokenBytes)
	if !scanner.Scan() {
		return item, scanner.Err()
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil || getString(obj, "type") != "session_meta" {
		return item, nil
	}
	payload := getMap(obj, "payload")
	if payload == nil {
		return item, nil
	}
	item.sessionID = getString(payload, "id")
	item.parentID = firstNonEmpty(
		getString(payload, "forked_from_id"),
		getNestedString(payload, "source", "subagent", "thread_spawn", "parent_thread_id"),
	)
	item.forkedAtMs = extractCodexTimestamp(obj)
	return item, nil
}

func codexIdentity(path string) (codexFileIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return codexFileIdentity{}, err
	}
	return codexFileIdentity{size: info.Size(), modTimeNS: info.ModTime().UnixNano()}, nil
}

func codexSessionPathRank(path string) int {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		switch filepath.Base(current) {
		case "sessions":
			return 0
		case "archived_sessions":
			return 1
		}
		parent := filepath.Dir(current)
		if parent == current {
			return 2
		}
	}
}

func readCodexReplayUsage(path string) ([]codexReplayUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, codexScannerInitialBufferBytes), codexScannerMaxTokenBytes)
	var result []codexReplayUsage
	state := codexReplayUsageState{}
	for scanner.Scan() {
		var obj map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			continue
		}
		if usage, status := extractCodexReplayUsage(obj, &state); status == codexReplayUsageComparable {
			result = append(result, usage)
		}
	}
	return result, scanner.Err()
}

func detectCodexRewrittenBurst(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, codexScannerInitialBufferBytes), codexScannerMaxTokenBytes)
	state := codexReplayUsageState{}
	var first codexReplayUsage
	hasFirst := false
	for scanner.Scan() {
		var obj map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			continue
		}
		usage, status := extractCodexReplayUsage(obj, &state)
		if status != codexReplayUsageComparable {
			continue
		}
		if !hasFirst {
			first = usage
			hasFirst = true
			continue
		}
		delta := usage.TimestampMs - first.TimestampMs
		if first.TimestampMs > 0 && usage.TimestampMs > 0 && delta >= 0 && delta <= codexReplayBurstPauseMs {
			return first.TimestampMs, nil
		}
		return 0, nil
	}
	return 0, scanner.Err()
}

func codexReplayPrefixAt(stream []codexReplayUsage, forkedAtMs int64) ([]codexReplayUsage, bool) {
	if forkedAtMs <= 0 {
		return nil, false
	}
	for index, usage := range stream {
		if usage.TimestampMs <= 0 {
			return nil, false
		}
		if usage.TimestampMs > forkedAtMs {
			return append([]codexReplayUsage(nil), stream[:index]...), true
		}
	}
	return append([]codexReplayUsage(nil), stream...), true
}

type codexReplayUsageState struct {
	previous    codexUsageSnapshot
	hasPrevious bool
}

type codexReplayUsageStatus uint8

const (
	codexReplayUsageUncomparable codexReplayUsageStatus = iota
	codexReplayUsageUnchanged
	codexReplayUsageComparable
)

func extractCodexReplayUsage(obj map[string]interface{}, state *codexReplayUsageState) (codexReplayUsage, codexReplayUsageStatus) {
	if getString(obj, "type") != "event_msg" || getNestedString(obj, "payload", "type") != "token_count" {
		return codexReplayUsage{}, codexReplayUsageUncomparable
	}
	info := getMap(getMap(obj, "payload"), "info")
	if info == nil {
		return codexReplayUsage{}, codexReplayUsageUncomparable
	}
	var raw codexUsageSnapshot
	if totalMap := getMap(info, "total_token_usage"); totalMap != nil {
		current := codexUsageFromMap(totalMap)
		advanced := !state.hasPrevious || !codexUsageSnapshotEqual(state.previous, current)
		if advanced {
			if lastMap := getMap(info, "last_token_usage"); lastMap != nil {
				raw = codexUsageFromMap(lastMap)
			}
			if raw.isZero() {
				// A missing or all-zero last_token_usage cannot describe an
				// advanced cumulative snapshot. Use the same reset-aware delta as
				// ledger accounting so a compact/counter reset remains comparable.
				raw = current.telescopingDelta(state.previous)
			}
		}
		state.previous = current
		state.hasPrevious = true
		if !advanced {
			return codexReplayUsage{}, codexReplayUsageUnchanged
		}
	} else if lastMap := getMap(info, "last_token_usage"); lastMap != nil {
		raw = codexUsageFromMap(lastMap)
	} else {
		return codexReplayUsage{}, codexReplayUsageUncomparable
	}
	if raw.isZero() {
		return codexReplayUsage{}, codexReplayUsageUncomparable
	}
	return codexReplayUsage{
		TimestampMs: extractCodexTimestamp(obj),
		Input:       raw.Input,
		Cached:      minInt64(raw.CachedInput, raw.Input),
		Output:      raw.Output,
		Reasoning:   raw.Reasoning,
		Total:       codexReplayTotalTokens(raw),
	}, codexReplayUsageComparable
}

func codexReplayTotalTokens(usage codexUsageSnapshot) int64 {
	if usage.HasTotal && usage.Total > 0 {
		return usage.Total
	}
	// Codex input already includes cached input and output already includes
	// hidden reasoning, so neither cached nor reasoning is added again.
	return usage.Input + usage.Output
}

func codexReplayUsageEqual(left, right codexReplayUsage) bool {
	return left.Input == right.Input &&
		left.Cached == right.Cached &&
		left.Output == right.Output &&
		left.Reasoning == right.Reasoning &&
		left.Total == right.Total
}
