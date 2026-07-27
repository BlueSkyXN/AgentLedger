package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const workBuddyAccountingProfile = "workbuddy_raw_usage_v1"

// WorkBuddyAdapter imports per-request provider usage envelopes emitted in
// WorkBuddy project JSONL files. It deliberately never retains message content
// or WorkBuddy credit balances in its raw diagnostic envelope.
type WorkBuddyAdapter struct{}

func NewWorkBuddyAdapter() *WorkBuddyAdapter { return &WorkBuddyAdapter{} }

func (a *WorkBuddyAdapter) Name() string { return "workbuddy" }

func (a *WorkBuddyAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.workbuddy/projects"}
	}

	files := make([]string, 0)
	seen := make(map[string]struct{})
	for _, configuredPath := range paths {
		base := expandHome(configuredPath)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if workBuddyExcludedDir(info.Name()) && path != base {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".jsonl") {
				cleaned := filepath.Clean(path)
				if _, ok := seen[cleaned]; !ok {
					seen[cleaned] = struct{}{}
					files = append(files, cleaned)
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk WorkBuddy source %s: %w", base, err)
		}
	}
	return files, nil
}

func workBuddyExcludedDir(name string) bool {
	switch strings.ToLower(name) {
	case "logs", "traces", "audit-log", "file-history", "blobs", "tool-output", "tool-outputs":
		return true
	default:
		return false
	}
}

func (a *WorkBuddyAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *WorkBuddyAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open WorkBuddy source %s: %w", path, err)
	}
	defer f.Close()

	records := make([]*fingerprint.ParsedRecord, 0)
	diagnostics := newWorkBuddyParseDiagnostics()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		obj, ok := decodeWorkBuddyObject(line)
		if !ok {
			// A malformed or non-usage JSONL entry must not prevent subsequent
			// request usage entries in the same session from being imported.
			diagnostics.add(lineNumber, "invalid_json")
			continue
		}
		record, reason := workBuddyRecordFromObject(obj, path, lineNumber, line)
		if record != nil {
			records = append(records, record)
		} else if reason != "" {
			diagnostics.add(lineNumber, reason)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, diagnostics.warnings(), fmt.Errorf("scan WorkBuddy source %s: %w", path, err)
	}
	return records, diagnostics.warnings(), nil
}

func decodeWorkBuddyObject(line []byte) (map[string]interface{}, bool) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var obj map[string]interface{}
	if err := decoder.Decode(&obj); err != nil || obj == nil {
		return nil, false
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return obj, true
}

func workBuddyRecordFromObject(obj map[string]interface{}, path string, lineNumber int, sourceLine []byte) (*fingerprint.ParsedRecord, string) {
	providerData := getMap(obj, "providerData")
	if providerData == nil {
		return nil, ""
	}
	usage := getMap(providerData, "usage")
	rawUsage := getMap(providerData, "rawUsage")
	if usage == nil && rawUsage == nil {
		return nil, ""
	}

	id := getString(obj, "id")
	sessionID := getString(obj, "sessionId")
	projectPath := getString(obj, "cwd")
	timestamp, hasTimestamp := strictWorkBuddyInt64(obj, "timestamp")
	if id == "" || sessionID == "" || projectPath == "" || !hasTimestamp {
		return nil, "missing_required_fields"
	}

	modelRaw := getString(providerData, "model")
	if modelRaw == "" || usage == nil || rawUsage == nil {
		return nil, "missing_required_fields"
	}
	requests, ok := strictWorkBuddyInt64(usage, "requests")
	if !ok || requests != 1 {
		return nil, "invalid_request_count"
	}

	prompt, hasPrompt := strictWorkBuddyInt64(rawUsage, "prompt_tokens")
	completion, hasCompletion := strictWorkBuddyInt64(rawUsage, "completion_tokens")
	total, hasTotal := strictWorkBuddyInt64(rawUsage, "total_tokens")
	if !hasPrompt || !hasCompletion || !hasTotal || prompt < 0 || completion < 0 || total <= 0 || total != prompt+completion {
		return nil, "invalid_token_totals"
	}

	cacheRead, hasCacheRead := workBuddyDetailInt64(rawUsage, "prompt_tokens_details", "cached_tokens")
	if !hasCacheRead {
		cacheRead = 0
	}
	cacheWrite, hasCacheWrite := strictWorkBuddyInt64(rawUsage, "prompt_cache_write_tokens")
	if !hasCacheWrite {
		cacheWrite = 0
	}
	reasoning, hasReasoning := workBuddyDetailInt64(rawUsage, "completion_tokens_details", "reasoning_tokens")
	if !hasReasoning {
		reasoning = 0
	}
	if cacheRead < 0 || cacheWrite < 0 || cacheRead+cacheWrite > prompt || reasoning < 0 || reasoning > completion {
		return nil, "invalid_token_details"
	}

	requestModelID := getString(providerData, "requestModelId")
	requestModelName := getString(providerData, "requestModelName")
	routeKind := workBuddyRouteKind(requestModelID)
	modelNormalized := normalizeWorkBuddyModel(modelRaw)
	modelResolution := model.ModelResolutionDirectEvent
	modelIsFallback := false
	if strings.EqualFold(strings.TrimSpace(modelNormalized), "auto") {
		modelNormalized = "unknown"
		modelResolution = model.ModelResolutionUnknown
		modelIsFallback = true
	}
	observability := "full"
	if !hasCacheRead || !hasCacheWrite || !hasReasoning {
		observability = "partial"
	}

	rawJSON, err := json.Marshal(workBuddyUsageEnvelope(requestModelID, requestModelName, modelRaw, routeKind, prompt, completion, total, cacheRead, cacheWrite, reasoning, hasCacheRead, hasCacheWrite, hasReasoning))
	if err != nil {
		return nil, "raw_envelope_encoding_failed"
	}

	return &fingerprint.ParsedRecord{
		Agent:                 "workbuddy",
		Provider:              workBuddyProvider(routeKind),
		Model:                 modelRaw,
		ModelNormalized:       modelNormalized,
		ModelResolution:       modelResolution,
		ModelIsFallback:       modelIsFallback,
		TimestampMs:           normalizeEpoch(timestamp),
		SessionID:             sessionID,
		ProjectPath:           projectPath,
		DedupeID:              id,
		MessageID:             getString(providerData, "messageId"),
		RequestID:             id,
		TurnID:                getString(providerData, "conversationRequestId"),
		InputTokens:           prompt - cacheRead - cacheWrite,
		OutputTokens:          completion,
		CacheCreationTokens:   cacheWrite,
		CacheReadTokens:       cacheRead,
		ReasoningTokens:       reasoning,
		TotalTokens:           total,
		SourceTotalTokens:     int64Ptr(total),
		RawInputTokens:        int64Ptr(prompt),
		SourceProduct:         "workbuddy",
		ObservabilityLevel:    observability,
		TokenAccountingMethod: model.AccWorkBuddyRawUsage,
		AccountingProfile:     workBuddyAccountingProfile,
		RawJSON:               string(rawJSON),
		SourceFile:            path,
		LineNumber:            lineNumber,
		RawSHA256:             sha256Hex(sourceLine),
	}, ""
}

type workBuddyParseDiagnostics struct {
	total   int
	counts  map[string]int
	samples []string
}

func newWorkBuddyParseDiagnostics() *workBuddyParseDiagnostics {
	return &workBuddyParseDiagnostics{counts: make(map[string]int)}
}

func (d *workBuddyParseDiagnostics) add(lineNumber int, reason string) {
	d.total++
	d.counts[reason]++
	if len(d.samples) < 5 {
		d.samples = append(d.samples, fmt.Sprintf("line %d %s", lineNumber, reason))
	}
}

func (d *workBuddyParseDiagnostics) warnings() []string {
	if d.total == 0 {
		return nil
	}
	reasons := make([]string, 0, len(d.counts))
	for reason := range d.counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	counts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		counts = append(counts, fmt.Sprintf("%s=%d", reason, d.counts[reason]))
	}
	warning := fmt.Sprintf("skipped %d invalid WorkBuddy line(s) (%s); samples: %s", d.total, strings.Join(counts, ", "), strings.Join(d.samples, ", "))
	if d.total > len(d.samples) {
		warning += fmt.Sprintf(", ... %d more", d.total-len(d.samples))
	}
	return []string{warning}
}

func strictWorkBuddyInt64(obj map[string]interface{}, key string) (int64, bool) {
	v, ok := obj[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case json.Number:
		value, err := n.Int64()
		return value, err == nil
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		if math.Trunc(n) != n || n > math.MaxInt64 || n < math.MinInt64 {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}

func workBuddyDetailInt64(obj map[string]interface{}, detailKey, valueKey string) (int64, bool) {
	detail := getMap(obj, detailKey)
	if detail == nil {
		return 0, false
	}
	return strictWorkBuddyInt64(detail, valueKey)
}

func workBuddyRouteKind(requestModelID string) string {
	if strings.HasPrefix(requestModelID, "custom-local:") {
		return "custom-local"
	}
	return "builtin"
}

func workBuddyProvider(routeKind string) string {
	if routeKind == "custom-local" {
		return "custom"
	}
	return "workbuddy"
}

func normalizeWorkBuddyModel(modelRaw string) string {
	switch strings.TrimSpace(modelRaw) {
	case "deepseek-v4-pro-202606":
		return "deepseek-v4-pro"
	case "k3", "kimi-k3-2":
		return "kimi-k3"
	default:
		return modelRaw
	}
}

func workBuddyUsageEnvelope(requestModelID, requestModelName, usageModel, routeKind string, prompt, completion, total, cacheRead, cacheWrite, reasoning int64, hasCacheRead, hasCacheWrite, hasReasoning bool) map[string]interface{} {
	envelope := map[string]interface{}{
		"request_model_id":   requestModelID,
		"request_model_name": requestModelName,
		"usage_model":        usageModel,
		"route_kind":         routeKind,
		"prompt_tokens":      prompt,
		"completion_tokens":  completion,
		"total_tokens":       total,
	}
	if hasCacheRead {
		envelope["cached_tokens"] = cacheRead
	}
	if hasCacheWrite {
		envelope["cache_write_tokens"] = cacheWrite
	}
	if hasReasoning {
		envelope["reasoning_tokens"] = reasoning
	}
	return envelope
}
