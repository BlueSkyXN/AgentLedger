package adapters

import (
	"bufio"
	"encoding/json"
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
	TaskCompleteWithTiming int
	TaskCompleteWithTTFT   int

	LedgerStats            CodexRecordStats
	CCUsageCompatibleStats CodexRecordStats
}

type CodexRecordStats struct {
	Events              int
	TotalTokens         int64
	InputTokens         int64
	RawInputTokens      int64
	OutputTokens        int64
	CacheReadTokens     int64
	ReasoningTokens     int64
	TotalDurationEvents int
	TTFTEvents          int
	OutputTPSEvents     int
	ModelCounts         map[string]int
}

type CodexModelCount struct {
	Model string
	Count int
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
	diag := &CodexDiagnostics{
		DuplicatePolicy:   normalizedPolicy,
		Paths:             normalizeCodexDiscoverPaths(paths),
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

	ledgerAdapter := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyLedger})
	ccusageAdapter := NewCodexAdapterWithOptions(CodexOptions{DuplicatePolicy: CodexDuplicatePolicyCCUsageCompatible})
	ledgerSeen := map[string]bool{}
	ccusageSeen := map[string]bool{}
	for _, file := range files {
		if err := scanCodexDiagnosticFile(file, diag); err != nil {
			return nil, err
		}
		ledgerRecords, err := ledgerAdapter.ParseFile(file)
		if err != nil {
			return nil, err
		}
		addCodexRecordStats(&diag.LedgerStats, ledgerRecords, ledgerSeen)

		ccusageRecords, err := ccusageAdapter.ParseFile(file)
		if err != nil {
			return nil, err
		}
		addCodexRecordStats(&diag.CCUsageCompatibleStats, ccusageRecords, ccusageSeen)
	}
	return diag, nil
}

func scanCodexDiagnosticFile(path string, diag *CodexDiagnostics) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
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
			if getInt64(payload, "duration_ms") > 0 || parseTimestamp(payload["completed_at"]) > 0 {
				diag.TaskCompleteWithTiming++
			}
			if getInt64(payload, "time_to_first_token_ms") > 0 {
				diag.TaskCompleteWithTTFT++
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
		eventID, _ := fingerprint.Compute(rec)
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
		if rec.TotalDurationMs > 0 {
			stats.TotalDurationEvents++
		}
		if rec.TTFTMs > 0 {
			stats.TTFTEvents++
		}
		outputDurationMs := rec.OutputDurationMs
		if outputDurationMs == 0 && rec.FirstTokenAtMs > 0 && rec.CompletedAtMs > 0 {
			outputDurationMs = saturatingSub(rec.CompletedAtMs, rec.FirstTokenAtMs)
		}
		if outputDurationMs == 0 && rec.TotalDurationMs > 0 && rec.TTFTMs > 0 {
			outputDurationMs = saturatingSub(rec.TotalDurationMs, rec.TTFTMs)
		}
		if outputDurationMs > 0 && rec.OutputTokens > 0 {
			stats.OutputTPSEvents++
		}
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

func (s CodexRecordStats) TimingCoverage() (totalDuration, ttft, tps float64) {
	if s.Events == 0 {
		return 0, 0, 0
	}
	events := float64(s.Events)
	return float64(s.TotalDurationEvents) / events, float64(s.TTFTEvents) / events, float64(s.OutputTPSEvents) / events
}
