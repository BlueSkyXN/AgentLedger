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

type GeminiAdapter struct{}

func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{}
}

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.gemini"}
	}
	return DiscoverFiles(paths, []string{".json", ".jsonl"})
}

func (a *GeminiAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".jsonl" {
		return parseGeminiJSONL(path)
	}
	return parseGeminiJSON(path)
}

func parseGeminiJSON(path string) ([]*fingerprint.ParsedRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		var arr []map[string]interface{}
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, nil
		}
		var records []*fingerprint.ParsedRecord
		for i, item := range arr {
			if rec := parseGeminiObject(item, path, i+1); rec != nil {
				records = append(records, rec)
			}
		}
		return records, nil
	}

	rec := parseGeminiObject(obj, path, 1)
	if rec != nil {
		return []*fingerprint.ParsedRecord{rec}, nil
	}
	return nil, nil
}

func parseGeminiJSONL(path string) ([]*fingerprint.ParsedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var records []*fingerprint.ParsedRecord
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

		if rec := parseGeminiObject(obj, path, lineNum); rec != nil {
			records = append(records, rec)
		}
	}

	return records, scanner.Err()
}

func parseGeminiObject(obj map[string]interface{}, path string, lineNum int) *fingerprint.ParsedRecord {
	usage := getMap(obj, "usageMetadata")
	if usage == nil {
		resp := getMap(obj, "response")
		if resp != nil {
			usage = getMap(resp, "usageMetadata")
		}
	}
	if usage == nil {
		return nil
	}

	rawJSON, _ := json.Marshal(obj)
	rawHash := sha256Hex(rawJSON)
	nativeSessionID := firstNonEmpty(
		getString(obj, "session_id"),
		getString(obj, "sessionId"),
		getNestedString(obj, "metadata", "session_id"),
		getNestedString(obj, "response", "session_id"),
	)
	nativeEventID, identityKind := geminiNativeIdentity(obj)
	identitySubkey := firstNonEmpty(getString(obj, "segment_id"), getString(obj, "part_id"))
	if nativeEventID == "" {
		identityKind = "record"
		identitySubkey = stableRecordIdentitySubkey(lineNum, identitySubkey)
	}
	sessionPathID := stableSessionPathID(path, ".gemini", "sessions", "projects")
	modelName := getString(obj, "model")
	if modelName == "" {
		if response := getMap(obj, "response"); response != nil {
			modelName = getString(response, "model")
		}
	}
	timestampMs := parseTimestamp(obj["timestamp"])
	if timestampMs == 0 {
		if response := getMap(obj, "response"); response != nil {
			timestampMs = parseTimestamp(response["timestamp"])
		}
	}
	prompt, hasPrompt := firstInt64Field(usage, "promptTokenCount", "prompt_token_count")
	output, _ := firstInt64Field(usage, "candidatesTokenCount", "candidates_token_count")
	cacheRead, _ := firstInt64Field(usage, "cachedContentTokenCount", "cached_content_token_count")
	reasoning, _ := firstInt64Field(usage, "thoughtsTokenCount", "thoughts_token_count")
	toolInput, hasToolInput := firstInt64Field(usage, "toolUsePromptTokenCount", "tool_use_prompt_token_count")
	total, hasTotal := firstInt64Field(usage, "totalTokenCount", "total_token_count")
	rawInput := prompt + toolInput

	record := &fingerprint.ParsedRecord{
		Agent:                 "gemini",
		Provider:              "google",
		Model:                 modelName,
		NativeSessionID:       nativeSessionID,
		SessionPathID:         sessionPathID,
		NativeEventID:         nativeEventID,
		IdentityKind:          identityKind,
		IdentityScope:         "session",
		IdentitySubkey:        identitySubkey,
		ParserVersion:         "gemini-v1",
		Granularity:           "request",
		TimestampMs:           timestampMs,
		SessionID:             firstNonEmpty(nativeSessionID, sessionPathID),
		InputTokens:           rawInput - cacheRead,
		OutputTokens:          output,
		CacheReadTokens:       cacheRead,
		ReasoningTokens:       reasoning,
		TotalTokens:           total,
		SourceProduct:         "gemini-cli",
		ObservabilityLevel:    "full",
		TokenAccountingMethod: model.AccGeminiUsage,
		AccountingProfile:     "gemini_usage_v1",
		FingerprintJSON:       string(rawJSON),
		SourceFile:            path,
		LineNumber:            lineNum,
		RawSHA256:             rawHash,
	}
	if hasTotal {
		record.SourceTotalTokens = int64Ptr(total)
	}
	if hasPrompt || hasToolInput {
		record.RawInputTokens = int64Ptr(rawInput)
	}
	return record
}

func geminiNativeIdentity(obj map[string]interface{}) (string, string) {
	for _, value := range []string{
		getString(obj, "event_id"),
		getString(obj, "eventId"),
		getString(obj, "id"),
		getNestedString(obj, "response", "event_id"),
		getNestedString(obj, "response", "eventId"),
		getNestedString(obj, "response", "id"),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), "event"
		}
	}
	for _, value := range []string{
		getString(obj, "message_id"),
		getString(obj, "messageId"),
		getNestedString(obj, "response", "message_id"),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), "message"
		}
	}
	for _, value := range []string{
		getString(obj, "request_id"),
		getString(obj, "requestId"),
		getNestedString(obj, "response", "request_id"),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), "request"
		}
	}
	return "", ""
}
