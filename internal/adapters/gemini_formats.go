package adapters

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type geminiParseState struct {
	sessionID string
	modelName string
	timestamp interface{}
	records   []*fingerprint.ParsedRecord
}

func (s *geminiParseState) consume(obj map[string]interface{}, path string, line int) {
	if session := firstNonEmpty(getString(obj, "sessionId"), getString(obj, "session_id")); session != "" {
		s.sessionID = session
	}
	if name := getString(obj, "model"); name != "" {
		s.modelName = name
	}
	if start, ok := obj["startTime"]; ok {
		s.timestamp = start
	}
	if set := getMap(obj, "$set"); set != nil {
		s.consume(set, path, line)
		return
	}
	if messages, ok := obj["messages"].([]interface{}); ok {
		for i, value := range messages {
			if message, ok := value.(map[string]interface{}); ok {
				s.consume(message, path, i+1)
			}
		}
		return
	}
	copy := make(map[string]interface{}, len(obj)+3)
	for key, value := range obj {
		copy[key] = value
	}
	if firstNonEmpty(getString(copy, "session_id"), getString(copy, "sessionId")) == "" && s.sessionID != "" {
		copy["session_id"] = s.sessionID
	}
	if getString(copy, "model") == "" {
		copy["model"] = s.modelName
	}
	if parseTimestamp(copy["timestamp"]) == 0 {
		if parseTimestamp(copy["created_at"]) > 0 {
			copy["timestamp"] = copy["created_at"]
		} else if parseTimestamp(s.timestamp) > 0 {
			copy["timestamp"] = s.timestamp
		}
	}
	if rec := parseGeminiObject(copy, path, line); rec != nil {
		s.records = append(s.records, rec)
		return
	}
	if getString(copy, "type") == "gemini" {
		if rec := geminiTokensRecord(copy, getMap(copy, "tokens"), path, line, false); rec != nil {
			s.records = append(s.records, rec)
		}
		return
	}
	stats := getMap(copy, "stats")
	if stats == nil {
		stats = getMap(getMap(copy, "result"), "stats")
	}
	if stats == nil {
		return
	}
	models := getMap(stats, "models")
	if models != nil {
		keys := make([]string, 0, len(models))
		for name := range models {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			data, _ := models[name].(map[string]interface{})
			copy["model"] = name
			copy["segment_id"] = "stats:" + name
			if rec := geminiTokensRecord(copy, getMap(data, "tokens"), path, line, true); rec != nil {
				s.records = append(s.records, rec)
			}
		}
		return
	}
	if rec := geminiTokensRecord(copy, stats, path, line, true); rec != nil {
		s.records = append(s.records, rec)
	}
}

func geminiTokensRecord(obj, tokens map[string]interface{}, path string, line int, summary bool) *fingerprint.ParsedRecord {
	if tokens == nil {
		return nil
	}
	input, hasInput := firstInt64Field(tokens, "input", "prompt", "input_tokens", "promptTokenCount")
	output, _ := firstInt64Field(tokens, "output", "candidates", "output_tokens", "candidatesTokenCount")
	cached, _ := firstInt64Field(tokens, "cached", "cache", "cached_tokens", "cachedContentTokenCount")
	thoughts, _ := firstInt64Field(tokens, "thoughts", "reasoning", "thoughtsTokenCount")
	tool, _ := firstInt64Field(tokens, "tool", "toolUsePromptTokenCount")
	total, hasTotal := firstInt64Field(tokens, "total", "total_tokens", "totalTokenCount")
	if !hasTotal {
		total = input + output + thoughts + tool
	}
	if input < 0 || output < 0 || cached < 0 || thoughts < 0 || tool < 0 || total < 0 {
		return nil
	}
	// CLI stats may report the uncached input, while saved chat messages report the full prompt.
	if summary && hasTotal && total == input+cached+output+thoughts+tool {
		input += cached
	}
	if !hasInput || total == 0 {
		return nil
	}
	usage := map[string]interface{}{"promptTokenCount": input, "candidatesTokenCount": output, "cachedContentTokenCount": cached, "thoughtsTokenCount": thoughts, "toolUsePromptTokenCount": tool, "totalTokenCount": total}
	obj["usageMetadata"] = usage
	rec := parseGeminiObject(obj, path, line)
	delete(obj, "usageMetadata")
	if rec == nil {
		return nil
	}
	rec.ParserVersion = "gemini-v2"
	if summary {
		rec.Granularity = "session_model"
		rec.ObservabilityLevel = "session_summary"
		rec.AccountingProfile = "gemini_stats_v1"
		if rec.NativeEventID == "" {
			rec.IdentityKind = "record"
			subkey, _ := json.Marshal([]string{"stats", rec.Model, fmt.Sprint(rec.TimestampMs)})
			rec.IdentitySubkey = string(subkey)
		}
	}
	raw, _ := json.Marshal(obj)
	rec.FingerprintJSON, rec.RawSHA256 = string(raw), sha256Hex(raw)
	return rec
}

func (a *GeminiAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	type scope struct{ session, model string }
	scopeFor := func(rec *fingerprint.ParsedRecord) scope {
		name, _, _ := NormalizeModelName(rec.Model)
		return scope{firstNonEmpty(rec.NativeSessionID, rec.SessionID, rec.SessionPathID), model.CanonicalModelID(name)}
	}
	requests := make(map[scope]int64)
	for _, rec := range records {
		if rec.Granularity == "request" && rec.TimestampMs > requests[scopeFor(rec)] &&
			rec.TotalTokens > 0 && rec.InputTokens >= 0 && rec.OutputTokens >= 0 && rec.ReasoningTokens >= 0 && rec.CacheReadTokens >= 0 &&
			rec.TotalTokens == rec.InputTokens+rec.OutputTokens+rec.ReasoningTokens+rec.CacheReadTokens {
			requests[scopeFor(rec)] = rec.TimestampMs
		}
	}
	result := make([]*fingerprint.ParsedRecord, 0, len(records))
	indexes := make(map[string]int)
	for _, rec := range records {
		if rec.Granularity == "session_model" && requests[scopeFor(rec)] > 0 {
			continue
		}
		_, id, _, _, err := fingerprint.ComputeIdentity(rec)
		if err != nil {
			result = append(result, rec)
			continue
		}
		if index, ok := indexes[id]; ok {
			if rec.TotalTokens > result[index].TotalTokens {
				result[index] = rec
			}
			continue
		}
		indexes[id] = len(result)
		result = append(result, rec)
	}
	return result
}
