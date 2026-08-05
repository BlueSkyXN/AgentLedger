package adapters

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
)

type CodexDiagnostics struct {
	DuplicatePolicy string
	Paths           []string
	Files           int
	Lines           int
	BadJSON         int

	TypeCounts        map[string]int
	PayloadTypeCounts map[string]int

	TokenCountEvents        int
	LastTokenUsageEvents    int
	TotalTokenUsageEvents   int
	LastAndTotalUsageEvents int
	TotalOnlyUsageEvents    int
	AllZeroUsageEvents      int

	TaskCompleteEvents     int
	TaskCompleteWithTurnID int

	LedgerStats            CodexRecordStats
	CCUsageCompatibleStats CodexRecordStats
	ReplayDiagnostics      []ImportDiagnostic
}

type CodexRecordStats struct {
	Events          int
	TotalTokens     int64
	InputTokens     int64
	RawInputTokens  int64
	OutputTokens    int64
	CacheReadTokens int64
	ReasoningTokens int64
	ModelCounts     map[string]int
}

type CodexModelCount struct {
	Model string
	Count int
}

type codexDiagnosticSnapshot map[string]codexFileIdentity

type codexReplayFileOutcome struct {
	quarantined     bool
	quarantine      string
	exactEvents     int64
	exactTokens     int64
	rewrittenEvents int64
	rewrittenTokens int64
	fileChanged     int64
}

func AnalyzeCodex(paths []string, duplicatePolicy string) (*CodexDiagnostics, error) {
	normalizedPolicy := normalizeCodexDuplicatePolicy(duplicatePolicy)
	if len(paths) == 0 {
		paths = []string{"~/.codex/sessions"}
	}
	discoverer := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: normalizedPolicy})
	files, err := discoverer.Discover(paths)
	if err != nil {
		return nil, err
	}
	return analyzeCodexDiscoveredFiles(files, normalizeCodexDiscoverPaths(paths), normalizedPolicy, nil)
}

func analyzeCodexDiscoveredFiles(
	files []string,
	displayPaths []string,
	normalizedPolicy string,
	betweenPolicies func(string),
) (*CodexDiagnostics, error) {
	diag := &CodexDiagnostics{
		DuplicatePolicy:   normalizedPolicy,
		Paths:             displayPaths,
		Files:             len(files),
		TypeCounts:        map[string]int{},
		PayloadTypeCounts: map[string]int{},
		LedgerStats: CodexRecordStats{
			ModelCounts: map[string]int{},
		},
		CCUsageCompatibleStats: CodexRecordStats{
			ModelCounts: map[string]int{},
		},
	}

	ledgerAdapter, ccusageAdapter, snapshot, err := prepareCodexDiagnosticComparison(files)
	if err != nil {
		return nil, err
	}
	ledgerSeen := map[string]bool{}
	ccusageSeen := map[string]bool{}
	ledgerOutcomes := make([]codexReplayFileOutcome, 0, len(files))
	ccusageOutcomes := make([]codexReplayFileOutcome, 0, len(files))
	for _, file := range files {
		if err := validateCodexDiagnosticFile(snapshot, file); err != nil {
			return nil, err
		}
		if err := scanCodexDiagnosticFile(file, diag); err != nil {
			return nil, err
		}
		if err := validateCodexDiagnosticFile(snapshot, file); err != nil {
			return nil, err
		}

		ledgerBefore := ledgerAdapter.ImportDiagnostics()
		ledgerRecords, err := ledgerAdapter.ParseFile(file)
		if err != nil && !isCodexReplayQuarantineError(err) {
			return nil, err
		}
		ledgerOutcomes = append(ledgerOutcomes, codexReplayOutcomeDelta(ledgerBefore, ledgerAdapter.ImportDiagnostics(), err))
		if err == nil {
			addCodexRecordStats(&diag.LedgerStats, ledgerRecords, ledgerSeen)
		}

		if betweenPolicies != nil {
			betweenPolicies(file)
		}
		if err := validateCodexDiagnosticFile(snapshot, file); err != nil {
			return nil, err
		}

		ccusageBefore := ccusageAdapter.ImportDiagnostics()
		ccusageRecords, err := ccusageAdapter.ParseFile(file)
		if err != nil && !isCodexReplayQuarantineError(err) {
			return nil, err
		}
		ccusageOutcomes = append(ccusageOutcomes, codexReplayOutcomeDelta(ccusageBefore, ccusageAdapter.ImportDiagnostics(), err))
		if err == nil {
			addCodexRecordStats(&diag.CCUsageCompatibleStats, ccusageRecords, ccusageSeen)
		}
		if err := validateCodexDiagnosticFile(snapshot, file); err != nil {
			return nil, err
		}
	}
	if err := validateCodexDiagnosticSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := validateCodexReplayPolicyOutcomes(ledgerOutcomes, ccusageOutcomes); err != nil {
		return nil, err
	}
	diag.ReplayDiagnostics = ledgerAdapter.ImportDiagnostics()
	if err := validateCodexReplayDiagnostics(diag.ReplayDiagnostics, ccusageAdapter.ImportDiagnostics()); err != nil {
		return nil, err
	}
	return diag, nil
}

func prepareCodexDiagnosticComparison(files []string) (*CodexAdapter, *CodexAdapter, codexDiagnosticSnapshot, error) {
	before, err := captureCodexDiagnosticSnapshot(files)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("prepare Codex diagnostic snapshot: %w", err)
	}
	plan, stats, err := buildCodexReplayPlan(files)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("prepare Codex replay diagnostics: %w", err)
	}
	if err := validateCodexDiagnosticSnapshot(before); err != nil {
		return nil, nil, nil, err
	}
	ledger := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyLedger})
	ccusage := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyCCUsageCompatible})
	// The replay plan is immutable after construction. Sharing it ensures both
	// accounting policies compare the same parent/child relationships.
	ledger.replayPlan = plan
	ledger.replayStats = stats
	ccusage.replayPlan = plan
	ccusage.replayStats = stats
	return ledger, ccusage, before, nil
}

func captureCodexDiagnosticSnapshot(files []string) (codexDiagnosticSnapshot, error) {
	snapshot := make(codexDiagnosticSnapshot, len(files))
	for _, file := range files {
		canonical, err := canonicalCodexPath(file)
		if err != nil {
			return nil, err
		}
		identity, err := codexIdentity(canonical)
		if err != nil {
			return nil, err
		}
		snapshot[canonical] = identity
	}
	return snapshot, nil
}

func validateCodexDiagnosticSnapshot(snapshot codexDiagnosticSnapshot) error {
	for path, expected := range snapshot {
		if err := validateCodexDiagnosticIdentity(path, expected); err != nil {
			return err
		}
	}
	return nil
}

func validateCodexDiagnosticFile(snapshot codexDiagnosticSnapshot, file string) error {
	canonical, err := canonicalCodexPath(file)
	if err != nil {
		return fmt.Errorf("Codex policy comparison inconclusive: source snapshot changed after replay preparation")
	}
	expected, ok := snapshot[canonical]
	if !ok {
		return fmt.Errorf("Codex policy comparison inconclusive: source snapshot identity is unavailable")
	}
	return validateCodexDiagnosticIdentity(canonical, expected)
}

func validateCodexDiagnosticIdentity(path string, expected codexFileIdentity) error {
	actual, err := codexIdentity(path)
	if err != nil || actual != expected {
		return fmt.Errorf("Codex policy comparison inconclusive: source snapshot changed after replay preparation")
	}
	return nil
}

func isCodexReplayQuarantineError(err error) bool {
	var target *codexReplayQuarantineError
	return errors.As(err, &target)
}

func codexReplayOutcomeDelta(before, after []ImportDiagnostic, parseErr error) codexReplayFileOutcome {
	exactBefore, _ := importDiagnosticByCode(before, codexDiagnosticReplayExact)
	exactAfter, _ := importDiagnosticByCode(after, codexDiagnosticReplayExact)
	rewrittenBefore, _ := importDiagnosticByCode(before, codexDiagnosticReplayRewritten)
	rewrittenAfter, _ := importDiagnosticByCode(after, codexDiagnosticReplayRewritten)
	changedBefore, _ := importDiagnosticByCode(before, codexDiagnosticReplayFileChanged)
	changedAfter, _ := importDiagnosticByCode(after, codexDiagnosticReplayFileChanged)
	outcome := codexReplayFileOutcome{
		exactEvents:     exactAfter.Events - exactBefore.Events,
		exactTokens:     exactAfter.Tokens - exactBefore.Tokens,
		rewrittenEvents: rewrittenAfter.Events - rewrittenBefore.Events,
		rewrittenTokens: rewrittenAfter.Tokens - rewrittenBefore.Tokens,
		fileChanged:     diagnosticCount(changedAfter) - diagnosticCount(changedBefore),
	}
	if isCodexReplayQuarantineError(parseErr) {
		outcome.quarantined = true
		outcome.quarantine = parseErr.Error()
	}
	return outcome
}

func validateCodexReplayPolicyOutcomes(left, right []codexReplayFileOutcome) error {
	if len(left) != len(right) {
		return fmt.Errorf("Codex policy comparison inconclusive: replay outcome cardinality differs")
	}
	for index := range left {
		if left[index].fileChanged != 0 || right[index].fileChanged != 0 {
			return fmt.Errorf("Codex policy comparison inconclusive: replay file_changed outcome detected")
		}
		if left[index].quarantined != right[index].quarantined || left[index].quarantine != right[index].quarantine {
			return fmt.Errorf("Codex policy comparison inconclusive: replay quarantine outcome differs at file %d", index+1)
		}
		if left[index].exactEvents != right[index].exactEvents ||
			left[index].rewrittenEvents != right[index].rewrittenEvents {
			return fmt.Errorf("Codex policy comparison inconclusive: replay skip outcome differs at file %d", index+1)
		}
	}
	return nil
}

func validateCodexReplayDiagnostics(left, right []ImportDiagnostic) error {
	leftByCode := make(map[string]ImportDiagnostic, len(left))
	rightByCode := make(map[string]ImportDiagnostic, len(right))
	for _, diagnostic := range left {
		leftByCode[diagnostic.Code] = diagnostic
	}
	for _, diagnostic := range right {
		rightByCode[diagnostic.Code] = diagnostic
	}
	if len(leftByCode) != len(rightByCode) {
		return fmt.Errorf("Codex policy comparison inconclusive: replay diagnostic set differs")
	}
	for code, leftItem := range leftByCode {
		rightItem, ok := rightByCode[code]
		if !ok || leftItem.Unit != rightItem.Unit || leftItem.Count != rightItem.Count || leftItem.Events != rightItem.Events {
			return fmt.Errorf("Codex policy comparison inconclusive: replay diagnostic outcome differs")
		}
	}
	for _, diagnostics := range [][]ImportDiagnostic{left, right} {
		changed, _ := importDiagnosticByCode(diagnostics, codexDiagnosticReplayFileChanged)
		failed, _ := importDiagnosticByCode(diagnostics, codexDiagnosticReplayPlanFailed)
		if diagnosticCount(changed) != 0 || diagnosticCount(failed) != 0 {
			return fmt.Errorf("Codex policy comparison inconclusive: replay snapshot drift or plan failure detected")
		}
	}
	return nil
}

func codexReplaySkipDiagnosticsEqual(left, right []ImportDiagnostic) bool {
	return validateCodexReplayDiagnostics(left, right) == nil
}

func diagnosticCount(diagnostic ImportDiagnostic) int64 {
	if diagnostic.Count != 0 {
		return diagnostic.Count
	}
	return diagnostic.Events
}

func importDiagnosticByCode(diagnostics []ImportDiagnostic, code string) (ImportDiagnostic, bool) {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return diagnostic, true
		}
	}
	return ImportDiagnostic{}, false
}

func scanCodexDiagnosticFile(path string, diag *CodexDiagnostics) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, codexScannerInitialBufferBytes), codexScannerMaxTokenBytes)
	for scanner.Scan() {
		diag.Lines++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			diag.BadJSON++
			continue
		}
		entryType := getString(obj, "type")
		if entryType == "" {
			entryType = "unknown"
		}
		diag.TypeCounts[entryType]++

		payload := getMap(obj, "payload")
		payloadType := ""
		if payload != nil {
			payloadType = getString(payload, "type")
			if payloadType != "" {
				diag.PayloadTypeCounts[payloadType]++
			}
		}
		if entryType == "event_msg" && payloadType == "task_complete" {
			diag.TaskCompleteEvents++
			if getString(payload, "turn_id") != "" {
				diag.TaskCompleteWithTurnID++
			}
		}

		if entryType == "event_msg" && payloadType == "token_count" {
			diag.TokenCountEvents++
			info := getMap(payload, "info")
			if info != nil {
				lastUsage := getMap(info, "last_token_usage")
				totalUsage := getMap(info, "total_token_usage")
				if lastUsage != nil {
					diag.LastTokenUsageEvents++
				}
				if totalUsage != nil {
					diag.TotalTokenUsageEvents++
				}
				if lastUsage != nil && totalUsage != nil {
					diag.LastAndTotalUsageEvents++
				}
				if lastUsage == nil && totalUsage != nil {
					diag.TotalOnlyUsageEvents++
				}
			}
		}

		usage, _, _, ok, _ := extractCodexUsage(obj, entryType)
		if ok && usage.storageUsage().isZero() {
			diag.AllZeroUsageEvents++
		}
	}
	return scanner.Err()
}

func addCodexRecordStats(stats *CodexRecordStats, records []*fingerprint.ParsedRecord, seen map[string]bool) {
	if stats.ModelCounts == nil {
		stats.ModelCounts = map[string]int{}
	}
	for _, rec := range records {
		_, eventID, _, _, err := fingerprint.ComputeIdentity(rec)
		if err != nil {
			continue
		}
		if seen[eventID] {
			continue
		}
		seen[eventID] = true
		stats.Events++
		stats.TotalTokens += rec.TotalTokens
		stats.InputTokens += rec.InputTokens
		if rec.RawInputTokens != nil {
			stats.RawInputTokens += *rec.RawInputTokens
		}
		stats.OutputTokens += rec.OutputTokens
		stats.CacheReadTokens += rec.CacheReadTokens
		stats.ReasoningTokens += rec.ReasoningTokens
		modelName := rec.Model
		if modelName == "" {
			modelName = "unknown"
		}
		stats.ModelCounts[modelName]++
	}
}

func (d *CodexDiagnostics) ConfiguredStats() CodexRecordStats {
	if d.DuplicatePolicy == CodexDuplicatePolicyCCUsageCompatible {
		return d.CCUsageCompatibleStats
	}
	return d.LedgerStats
}

func (d *CodexDiagnostics) DuplicateDeltaEvents() int {
	return d.CCUsageCompatibleStats.Events - d.LedgerStats.Events
}

func (d *CodexDiagnostics) DuplicateDeltaTokens() int64 {
	return d.CCUsageCompatibleStats.TotalTokens - d.LedgerStats.TotalTokens
}

func (s CodexRecordStats) TopModels(limit int) []CodexModelCount {
	items := make([]CodexModelCount, 0, len(s.ModelCounts))
	for modelName, count := range s.ModelCounts {
		items = append(items, CodexModelCount{Model: modelName, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Model < items[j].Model
		}
		return items[i].Count > items[j].Count
	})
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}
