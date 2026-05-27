package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type CodexAdapter struct{}

type codexUsageSnapshot struct {
	Input          int64
	CachedInput    int64
	Output         int64
	Reasoning      int64
	Total          int64
	HasInput       bool
	HasCachedInput bool
	HasOutput      bool
	HasReasoning   bool
	HasTotal       bool
}

func NewCodexAdapter() *CodexAdapter {
	return &CodexAdapter{}
}

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.codex/sessions"}
	}
	return DiscoverFiles(paths, []string{".jsonl"})
}

func (a *CodexAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var records []*fingerprint.ParsedRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNum := 0
	defaultSessionID := extractCodexSession(path)
	currentModel := ""
	currentModelIsFallback := false
	previousTotals := map[string]codexUsageSnapshot{}
	seenLastUsageSnapshots := map[string]map[string]bool{}

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		entryType := getString(obj, "type")
		if entryType == "turn_context" {
			if modelName := extractCodexTurnContextModel(obj); modelName != "" {
				currentModel = modelName
				currentModelIsFallback = false
			}
			continue
		}

		usage, method, sourceUsage, ok := extractCodexUsage(obj, entryType)
		if !ok {
			continue
		}

		sessionID := extractCodexSessionID(obj, defaultSessionID)
		if method == model.AccCodexLastTokenUsage {
			if seenCodexLastUsageSnapshot(seenLastUsageSnapshots, sessionID, usage, sourceUsage) {
				if sourceUsage.HasTotal {
					previousTotals[sessionID] = sourceUsage
				}
				continue
			}
			if sourceUsage.HasTotal {
				previousTotals[sessionID] = sourceUsage
			}
		} else if method == model.AccCodexTotalDelta {
			previous := previousTotals[sessionID]
			usage = sourceUsage.delta(previous)
			previousTotals[sessionID] = sourceUsage
		}
		usage.CachedInput = minInt64(usage.CachedInput, usage.Input)
		if usage.isZero() {
			continue
		}

		parsedModel := extractCodexModel(obj)
		if parsedModel != "" {
			currentModel = parsedModel
			currentModelIsFallback = false
		}
		modelName := parsedModel
		modelIsFallback := false
		if modelName == "" && currentModel != "" {
			modelName = currentModel
			modelIsFallback = currentModelIsFallback
		}
		if modelName == "" {
			modelName = "gpt-5"
			modelIsFallback = true
			currentModel = modelName
			currentModelIsFallback = true
		}

		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)
		totalTokens := usage.totalTokens()
		rec := &fingerprint.ParsedRecord{
			Agent:                 "codex",
			Provider:              "openai",
			Model:                 modelName,
			TimestampMs:           extractCodexTimestamp(obj),
			SessionID:             sessionID,
			MessageID:             firstNonEmpty(getString(obj, "id"), getString(obj, "message_id")),
			RequestID:             firstNonEmpty(getString(obj, "request_id"), getNestedString(obj, "payload", "request_id")),
			InputTokens:           usage.Input,
			OutputTokens:          usage.Output,
			CacheReadTokens:       usage.CachedInput,
			ReasoningTokens:       usage.Reasoning,
			TotalTokens:           totalTokens,
			SourceTotalTokens:     sourceUsage.sourceTotalPtr(),
			ObservabilityLevel:    "full",
			ModelIsFallback:       modelIsFallback,
			TokenAccountingMethod: method,
			RawJSON:               string(rawJSON),
			SourceFile:            path,
			LineNumber:            lineNum,
			RawSHA256:             rawHash,
		}
		records = append(records, rec)
	}

	return records, scanner.Err()
}

func extractCodexUsage(obj map[string]interface{}, entryType string) (codexUsageSnapshot, string, codexUsageSnapshot, bool) {
	if entryType == "event_msg" {
		payload := getMap(obj, "payload")
		if payload == nil || getString(payload, "type") != "token_count" {
			return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false
		}
		info := getMap(payload, "info")
		if info == nil {
			return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false
		}
		if usage := getMap(info, "last_token_usage"); usage != nil {
			snapshot := codexUsageFromMap(usage)
			sourceSnapshot := snapshot
			if totalUsage := getMap(info, "total_token_usage"); totalUsage != nil {
				sourceSnapshot = codexUsageFromMap(totalUsage)
			}
			return snapshot, model.AccCodexLastTokenUsage, sourceSnapshot, true
		}
		if usage := getMap(info, "total_token_usage"); usage != nil {
			snapshot := codexUsageFromMap(usage)
			return snapshot, model.AccCodexTotalDelta, snapshot, true
		}
		return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false
	}

	if entryType != "" && !hasHeadlessCodexUsage(obj) {
		return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false
	}
	for _, container := range []map[string]interface{}{obj, getMap(obj, "data"), getMap(obj, "result"), getMap(obj, "response")} {
		if container == nil {
			continue
		}
		if usage := getMap(container, "usage"); usage != nil {
			snapshot := codexUsageFromMap(usage)
			return snapshot, model.AccCodexHeadlessUsage, snapshot, true
		}
	}
	return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false
}

func hasHeadlessCodexUsage(obj map[string]interface{}) bool {
	if getMap(obj, "usage") != nil {
		return true
	}
	for _, key := range []string{"data", "result", "response"} {
		if nested := getMap(obj, key); nested != nil && getMap(nested, "usage") != nil {
			return true
		}
	}
	return false
}

func codexUsageFromMap(usage map[string]interface{}) codexUsageSnapshot {
	input, hasInput := firstInt64Field(usage, "input_tokens", "prompt_tokens", "input")
	cached, hasCached := firstInt64Field(usage, "cached_input_tokens", "cache_read_input_tokens", "cached_tokens")
	output, hasOutput := firstInt64Field(usage, "output_tokens", "completion_tokens", "output")
	reasoning, hasReasoning := firstInt64Field(usage, "reasoning_output_tokens", "reasoning_tokens")
	total, hasTotal := firstInt64Field(usage, "total_tokens")
	return codexUsageSnapshot{
		Input: input, CachedInput: cached, Output: output, Reasoning: reasoning, Total: total,
		HasInput: hasInput, HasCachedInput: hasCached, HasOutput: hasOutput, HasReasoning: hasReasoning, HasTotal: hasTotal,
	}
}

func (u codexUsageSnapshot) delta(previous codexUsageSnapshot) codexUsageSnapshot {
	return codexUsageSnapshot{
		Input:          saturatingSub(u.Input, previous.Input),
		CachedInput:    saturatingSub(u.CachedInput, previous.CachedInput),
		Output:         saturatingSub(u.Output, previous.Output),
		Reasoning:      saturatingSub(u.Reasoning, previous.Reasoning),
		Total:          saturatingSub(u.Total, previous.Total),
		HasInput:       u.HasInput,
		HasCachedInput: u.HasCachedInput,
		HasOutput:      u.HasOutput,
		HasReasoning:   u.HasReasoning,
		HasTotal:       u.HasTotal,
	}
}

func (u codexUsageSnapshot) isZero() bool {
	return u.Input == 0 && u.CachedInput == 0 && u.Output == 0 && u.Reasoning == 0 && u.Total == 0
}

func (u codexUsageSnapshot) totalTokens() int64 {
	if u.HasTotal {
		return u.Total
	}
	return u.Input + u.CachedInput + u.Output + u.Reasoning
}

func (u codexUsageSnapshot) sourceTotalPtr() *int64 {
	if !u.HasTotal {
		return nil
	}
	return int64Ptr(u.Total)
}

func seenCodexLastUsageSnapshot(seen map[string]map[string]bool, sessionID string, usage, source codexUsageSnapshot) bool {
	key := usage.snapshotKey() + "|" + source.snapshotKey()
	sessionSeen := seen[sessionID]
	if sessionSeen == nil {
		sessionSeen = map[string]bool{}
		seen[sessionID] = sessionSeen
	}
	if sessionSeen[key] {
		return true
	}
	sessionSeen[key] = true
	return false
}

func (u codexUsageSnapshot) snapshotKey() string {
	return fmt.Sprintf("%d/%d/%d/%d/%d/%t/%t/%t/%t/%t",
		u.Input, u.CachedInput, u.Output, u.Reasoning, u.Total,
		u.HasInput, u.HasCachedInput, u.HasOutput, u.HasReasoning, u.HasTotal)
}

func extractCodexModel(obj map[string]interface{}) string {
	for _, value := range []string{
		getString(obj, "model"),
		getString(obj, "model_name"),
		getNestedString(obj, "metadata", "model"),
		getNestedString(obj, "payload", "model"),
		getNestedString(obj, "payload", "model_name"),
		getNestedString(obj, "payload", "metadata", "model"),
		getNestedString(obj, "payload", "info", "model"),
		getNestedString(obj, "payload", "info", "model_name"),
		getNestedString(obj, "payload", "info", "metadata", "model"),
		getNestedString(obj, "data", "model"),
		getNestedString(obj, "data", "model_name"),
		getNestedString(obj, "data", "metadata", "model"),
		getNestedString(obj, "result", "model"),
		getNestedString(obj, "result", "model_name"),
		getNestedString(obj, "result", "metadata", "model"),
		getNestedString(obj, "response", "model"),
		getNestedString(obj, "response", "model_name"),
		getNestedString(obj, "response", "metadata", "model"),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractCodexTurnContextModel(obj map[string]interface{}) string {
	for _, value := range []string{
		getNestedString(obj, "payload", "model"),
		getNestedString(obj, "payload", "info", "model"),
		getNestedString(obj, "payload", "metadata", "model"),
		getNestedString(obj, "turn_context", "model"),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractCodexSessionID(obj map[string]interface{}, fallback string) string {
	for _, value := range []string{
		getString(obj, "session_id"),
		getString(obj, "sessionId"),
		getNestedString(obj, "payload", "session_id"),
		fallback,
	} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown"
}

func extractCodexTimestamp(obj map[string]interface{}) int64 {
	for _, key := range []string{"timestamp", "created_at", "createdAt"} {
		if ts := parseTimestamp(obj[key]); ts > 0 {
			return ts
		}
	}
	for _, key := range []string{"data", "result", "response"} {
		if nested := getMap(obj, key); nested != nil {
			for _, tsKey := range []string{"timestamp", "created_at", "createdAt"} {
				if ts := parseTimestamp(nested[tsKey]); ts > 0 {
					return ts
				}
			}
		}
	}
	return 0
}

func extractCodexSession(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func saturatingSub(current, previous int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
