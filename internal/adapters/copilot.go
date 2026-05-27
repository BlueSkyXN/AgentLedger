package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	ledgermodel "github.com/BlueSkyXN/AgentLedger/internal/model"
)

type CopilotAdapter struct{}

func NewCopilotAdapter() *CopilotAdapter {
	return &CopilotAdapter{}
}

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.copilot/otel"}
	}
	files, err := DiscoverFiles(paths, []string{".jsonl"})
	if err != nil {
		return nil, err
	}
	if explicit := strings.TrimSpace(os.Getenv("COPILOT_OTEL_FILE_EXPORTER_PATH")); explicit != "" {
		files = append(files, expandHome(explicit))
	}
	return uniqueExistingFiles(files), nil
}

func (a *CopilotAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var candidates []copilotCandidate
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNum := 0
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
		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)
		candidates = append(candidates, copilotCandidatesFromObject(obj, string(rawJSON), rawHash, path, lineNum)...)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	candidates = dedupeCopilotCandidates(candidates)
	records := make([]*fingerprint.ParsedRecord, 0, len(candidates))
	for _, candidate := range candidates {
		records = append(records, candidate.record)
	}
	return records, nil
}

type copilotCandidate struct {
	record    *fingerprint.ParsedRecord
	eventType string
	priority  int
	score     int64
	key       string
}

func copilotCandidatesFromObject(obj map[string]interface{}, rawJSON, rawHash, path string, lineNum int) []copilotCandidate {
	baseAttrs := flattenCopilotAttributes(obj)
	var candidates []copilotCandidate
	if candidate := copilotCandidateFromAttrs(obj, baseAttrs, rawJSON, rawHash, path, lineNum); candidate.record != nil {
		candidates = append(candidates, candidate)
	}

	if events, ok := obj["events"].([]interface{}); ok {
		for _, item := range events {
			eventObj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			attrs := copyAttrs(baseAttrs)
			mergeAttrs(attrs, flattenCopilotAttributes(eventObj))
			if name := getString(eventObj, "name"); name != "" {
				attrs["event.name"] = name
			}
			if candidate := copilotCandidateFromAttrs(obj, attrs, rawJSON, rawHash, path, lineNum); candidate.record != nil {
				candidates = append(candidates, candidate)
			}
		}
	}
	return candidates
}

func copilotCandidateFromAttrs(obj, attrs map[string]interface{}, rawJSON, rawHash, path string, lineNum int) copilotCandidate {
	rawInput, hasInput := attrFirstInt64(attrs, "gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens")
	output, hasOutput := attrFirstInt64(attrs, "gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens")
	cacheRead, hasCacheRead := attrFirstInt64(attrs, "gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cached_input_tokens")
	cacheCreation, hasCacheCreation := attrFirstInt64(attrs, "gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_write.input_tokens")
	reasoning, hasReasoning := attrFirstInt64(attrs, "gen_ai.usage.reasoning.output_tokens", "gen_ai.usage.reasoning_tokens")
	sourceTotal, hasSourceTotal := attrFirstInt64(attrs, "gen_ai.usage.total_tokens", "gen_ai.usage.total.token_count")
	if !hasInput && !hasOutput && !hasCacheRead && !hasCacheCreation && !hasReasoning && !hasSourceTotal {
		return copilotCandidate{}
	}

	normalizedInput := rawInput
	if cacheRead > 0 {
		if cacheRead > rawInput {
			normalizedInput = 0
		} else {
			normalizedInput = rawInput - cacheRead
		}
	}
	computedTotal := normalizedInput + output + cacheRead + cacheCreation + reasoning
	if computedTotal == 0 && hasSourceTotal {
		computedTotal = sourceTotal
	}

	traceID := firstNonEmpty(getString(obj, "traceId"), getNestedString(obj, "spanContext", "traceId"), attrString(attrs, "traceId"), attrString(attrs, "spanContext.traceId"))
	spanID := firstNonEmpty(getString(obj, "spanId"), getNestedString(obj, "spanContext", "spanId"), attrString(attrs, "spanId"), attrString(attrs, "spanContext.spanId"))
	responseID := attrString(attrs, "gen_ai.response.id")
	interactionID := attrString(attrs, "github.copilot.interaction_id")
	dedupKey := ""
	if traceID != "" && spanID != "" {
		dedupKey = traceID + ":" + spanID
	} else if responseID != "" {
		dedupKey = responseID
	} else if interactionID != "" {
		dedupKey = interactionID
	}

	model := firstNonEmpty(attrString(attrs, "gen_ai.response.model"), attrString(attrs, "gen_ai.request.model"), getString(obj, "model"))
	sessionID := firstNonEmpty(
		attrString(attrs, "gen_ai.conversation.id"),
		attrString(attrs, "copilot_chat.session_id"),
		attrString(attrs, "copilot_chat.chat_session_id"),
		attrString(attrs, "session.id"),
		attrString(attrs, "github.copilot.interaction_id"),
	)
	eventType := inferCopilotEventType(obj, attrs)
	hasParts := hasInput || hasOutput || hasCacheRead || hasCacheCreation || hasReasoning
	observability := "full"
	accountingMethod := ledgermodel.AccCopilotOtelParts
	if !hasParts && hasSourceTotal {
		observability = "inferred"
		accountingMethod = ledgermodel.AccCopilotOtelTotalFallback
	}

	record := &fingerprint.ParsedRecord{
		Agent:                 "copilot",
		Provider:              "github",
		Model:                 model,
		TimestampMs:           extractCopilotTimestamp(obj),
		SessionID:             sessionID,
		MessageID:             dedupKey,
		RequestID:             firstNonEmpty(responseID, attrString(attrs, "gen_ai.request.id"), interactionID),
		InputTokens:           normalizedInput,
		OutputTokens:          output,
		CacheCreationTokens:   cacheCreation,
		CacheReadTokens:       cacheRead,
		ReasoningTokens:       reasoning,
		TotalTokens:           computedTotal,
		ObservabilityLevel:    observability,
		TokenAccountingMethod: accountingMethod,
		RawJSON:               rawJSON,
		SourceFile:            path,
		LineNumber:            lineNum,
		RawSHA256:             rawHash,
	}
	if hasSourceTotal {
		record.SourceTotalTokens = int64Ptr(sourceTotal)
	}
	return copilotCandidate{
		record:    record,
		eventType: eventType,
		priority:  copilotEventPriority(eventType),
		key:       dedupKey,
		score:     copilotCandidateScore(record),
	}
}

func dedupeCopilotCandidates(candidates []copilotCandidate) []copilotCandidate {
	selected := map[string]copilotCandidate{}
	var result []copilotCandidate
	for _, candidate := range candidates {
		if candidate.record == nil {
			continue
		}
		if candidate.key == "" {
			result = append(result, candidate)
			continue
		}
		existing, ok := selected[candidate.key]
		if !ok || betterCopilotCandidate(candidate, existing) {
			selected[candidate.key] = candidate
		}
	}
	for _, candidate := range candidates {
		if candidate.record == nil || candidate.key == "" {
			continue
		}
		selectedCandidate, ok := selected[candidate.key]
		if ok && selectedCandidate.record == candidate.record {
			result = append(result, candidate)
			delete(selected, candidate.key)
		}
	}
	return result
}

func betterCopilotCandidate(candidate, existing copilotCandidate) bool {
	if candidate.priority != existing.priority {
		return candidate.priority > existing.priority
	}
	return candidate.score > existing.score
}

func copilotEventPriority(eventType string) int {
	switch eventType {
	case "copilot_chat_span":
		return 4
	case "copilot_inference_log":
		return 3
	case "copilot_agent_turn":
		return 2
	case "copilot_agent_summary":
		return 1
	default:
		return 0
	}
}

func copilotCandidateScore(record *fingerprint.ParsedRecord) int64 {
	var score int64
	for _, value := range []int64{record.InputTokens, record.OutputTokens, record.CacheCreationTokens, record.CacheReadTokens, record.ReasoningTokens} {
		if value > 0 {
			score += 1_000_000
		}
	}
	score += record.TotalTokens
	return score
}

func flattenCopilotAttributes(obj map[string]interface{}) map[string]interface{} {
	attrs := map[string]interface{}{}
	mergeAttrs(attrs, parseAttributeValue(obj["attributes"]))
	if resource := getMap(obj, "resource"); resource != nil {
		mergeAttrs(attrs, parseAttributeValue(resource["attributes"]))
	}
	if scope := getMap(obj, "scope"); scope != nil {
		mergeAttrs(attrs, parseAttributeValue(scope["attributes"]))
	}
	return attrs
}

func parseAttributeValue(value interface{}) map[string]interface{} {
	attrs := map[string]interface{}{}
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, value := range typed {
			attrs[key] = otelValue(value)
		}
	case []interface{}:
		for _, item := range typed {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			key := getString(entry, "key")
			if key == "" {
				continue
			}
			attrs[key] = otelValue(entry["value"])
		}
	}
	return attrs
}

func otelValue(value interface{}) interface{} {
	m, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	for _, key := range []string{"stringValue", "intValue", "doubleValue", "boolValue"} {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return value
}

func mergeAttrs(dst, src map[string]interface{}) {
	for key, value := range src {
		dst[key] = value
	}
}

func copyAttrs(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	mergeAttrs(dst, src)
	return dst
}

func attrFirstInt64(attrs map[string]interface{}, keys ...string) (int64, bool) {
	for _, key := range keys {
		if value, ok := attrInt64(attrs, key); ok {
			return value, true
		}
	}
	return 0, false
}

func attrInt64(attrs map[string]interface{}, key string) (int64, bool) {
	if attrs == nil {
		return 0, false
	}
	return scalarInt64(attrs[key])
}

func scalarInt64(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		var number json.Number = json.Number(strings.TrimSpace(typed))
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func attrString(attrs map[string]interface{}, key string) string {
	if attrs == nil {
		return ""
	}
	if value, ok := attrs[key]; ok {
		switch typed := value.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		}
	}
	return ""
}

func inferCopilotEventType(obj, attrs map[string]interface{}) string {
	name := strings.ToLower(firstNonEmpty(getString(obj, "name"), getString(obj, "eventName"), attrString(attrs, "event.name"), attrString(attrs, "name")))
	switch {
	case strings.Contains(name, "summary"):
		return "copilot_agent_summary"
	case strings.Contains(name, "turn"):
		return "copilot_agent_turn"
	case strings.Contains(name, "inference"):
		return "copilot_inference_log"
	case strings.Contains(name, "chat") || getString(obj, "spanId") != "" || getNestedString(obj, "spanContext", "spanId") != "":
		return "copilot_chat_span"
	default:
		return "copilot_inference_log"
	}
}

func extractCopilotTimestamp(obj map[string]interface{}) int64 {
	for _, key := range []string{"timestamp", "time", "startTime", "endTime"} {
		if ts := parseTimestamp(obj[key]); ts > 0 {
			return ts
		}
	}
	for _, key := range []string{"timeUnixNano", "startTimeUnixNano", "endTimeUnixNano"} {
		if value, ok := scalarInt64(obj[key]); ok && value > 0 {
			return value / 1_000_000
		}
	}
	return 0
}

func uniqueExistingFiles(paths []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, path := range paths {
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}
