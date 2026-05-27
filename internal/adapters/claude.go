package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

type ClaudeAdapter struct{}

type claudeUsageCandidate struct {
	root        map[string]interface{}
	message     map[string]interface{}
	usage       map[string]interface{}
	timestamp   interface{}
	sessionID   string
	requestID   string
	uuid        string
	messageID   string
	model       string
	costUSD     *float64
	isSidechain bool
}

func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{}
}

func (a *ClaudeAdapter) Name() string { return "claude" }

func (a *ClaudeAdapter) Discover(paths []string) ([]string, error) {
	return DiscoverFiles(normalizeClaudeDiscoverPaths(paths), []string{".jsonl"})
}

func (a *ClaudeAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
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
		if hasClaudeUnsupportedNullField(line) {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		candidate, ok := claudeUsageFromObject(obj)
		if !ok {
			continue
		}

		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)
		sessionID := candidate.sessionID
		if sessionID == "" {
			sessionID = extractClaudeSession(path)
		}
		modelName := normalizeClaudeModel(candidate.model, getString(candidate.usage, "speed"))
		totalTokens := claudeUsageTotal(candidate.usage)
		if candidate.model == "<synthetic>" && totalTokens == 0 {
			continue
		}
		messageID := candidate.messageID
		dedupeRequestID := candidate.requestID
		if messageID == "" {
			messageID = candidate.uuid
			dedupeRequestID = ""
		}
		dedupeID := claudeDedupeID(messageID, dedupeRequestID)

		rec := &fingerprint.ParsedRecord{
			Agent:                 "claude",
			Provider:              "anthropic",
			Model:                 modelName,
			TimestampMs:           parseTimestamp(candidate.timestamp),
			SessionID:             sessionID,
			ProjectPath:           extractClaudeProject(path),
			DedupeID:              dedupeID,
			MessageID:             messageID,
			RequestID:             candidate.requestID,
			InputTokens:           getInt64(candidate.usage, "input_tokens"),
			OutputTokens:          getInt64(candidate.usage, "output_tokens"),
			CacheCreationTokens:   getInt64(candidate.usage, "cache_creation_input_tokens"),
			CacheReadTokens:       getInt64(candidate.usage, "cache_read_input_tokens"),
			CostUSD:               candidate.costUSD,
			IsSidechain:           candidate.isSidechain,
			UsageSpeed:            getString(candidate.usage, "speed"),
			TokenAccountingMethod: model.AccClaudeUsageSum,
			// TotalTokens left as 0 — import.go computes the full sum (incl. cache) consistently with other adapters
			RawJSON:    string(rawJSON),
			SourceFile: path,
			LineNumber: lineNum,
			RawSHA256:  rawHash,
		}

		records = append(records, rec)
	}

	return records, scanner.Err()
}

func (a *ClaudeAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	deduped := make([]*fingerprint.ParsedRecord, 0, len(records))
	indexes := make(map[string][]int, len(records))

	for _, rec := range records {
		if rec == nil {
			continue
		}
		indexKey := ""
		existingIndex := -1
		if rec.MessageID != "" {
			indexKey = claudeDedupeID(rec.MessageID, rec.RequestID)
			for _, index := range indexes[indexKey] {
				if claudeRecordsMatchDedupeKey(deduped[index], rec.MessageID, rec.RequestID) {
					existingIndex = index
					break
				}
			}
			if existingIndex < 0 {
				messageKey := claudeDedupeID(rec.MessageID, "")
				for _, index := range indexes[messageKey] {
					if claudeRecordsMatchSidechainDedupeKey(deduped[index], rec.MessageID, rec.IsSidechain) {
						existingIndex = index
						break
					}
				}
			}
		}

		if existingIndex >= 0 {
			if shouldReplaceClaudeRecord(rec, deduped[existingIndex]) {
				deduped[existingIndex] = rec
				if indexKey != "" {
					addClaudeDedupeIndex(indexes, indexKey, existingIndex)
					addClaudeDedupeIndex(indexes, claudeDedupeID(rec.MessageID, ""), existingIndex)
				}
			}
			continue
		}

		index := len(deduped)
		deduped = append(deduped, rec)
		if indexKey != "" {
			addClaudeDedupeIndex(indexes, indexKey, index)
			addClaudeDedupeIndex(indexes, claudeDedupeID(rec.MessageID, ""), index)
		}
	}
	return deduped
}

func claudeUsageFromObject(obj map[string]interface{}) (claudeUsageCandidate, bool) {
	if msg := getMap(obj, "message"); msg != nil {
		usage := getMap(obj, "usage")
		if usage == nil {
			usage = getMap(msg, "usage")
		}
		if usage != nil {
			return claudeUsageCandidate{
				root:        obj,
				message:     msg,
				usage:       usage,
				timestamp:   obj["timestamp"],
				sessionID:   getString(obj, "sessionId"),
				requestID:   getString(obj, "requestId"),
				uuid:        getString(obj, "uuid"),
				messageID:   getString(msg, "id"),
				model:       firstNonEmpty(getString(obj, "model"), getString(msg, "model")),
				costUSD:     optionalFloat64(obj, "costUSD"),
				isSidechain: getBool(obj, "isSidechain"),
			}, true
		}
	}

	wrapper := getMap(getMap(obj, "data"), "message")
	if wrapper == nil {
		return claudeUsageCandidate{}, false
	}
	msg := getMap(wrapper, "message")
	if msg == nil {
		return claudeUsageCandidate{}, false
	}
	usage := getMap(msg, "usage")
	if usage == nil {
		return claudeUsageCandidate{}, false
	}
	return claudeUsageCandidate{
		root:        obj,
		message:     msg,
		usage:       usage,
		timestamp:   wrapper["timestamp"],
		requestID:   getString(wrapper, "requestId"),
		messageID:   getString(msg, "id"),
		model:       getString(msg, "model"),
		costUSD:     optionalFloat64(wrapper, "costUSD"),
		isSidechain: getBool(wrapper, "isSidechain"),
	}, true
}

func claudeUsageTotal(usage map[string]interface{}) int64 {
	return getInt64(usage, "input_tokens") +
		getInt64(usage, "output_tokens") +
		getInt64(usage, "cache_creation_input_tokens") +
		getInt64(usage, "cache_read_input_tokens")
}

func normalizeClaudeModel(model, speed string) string {
	if model == "<synthetic>" {
		return ""
	}
	if speed == "fast" && model != "" {
		return model + "-fast"
	}
	return model
}

func claudeDedupeID(messageID, requestID string) string {
	if messageID == "" {
		return ""
	}
	if requestID == "" {
		return messageID
	}
	return messageID + ":" + requestID
}

func claudeRecordsMatchDedupeKey(rec *fingerprint.ParsedRecord, messageID, requestID string) bool {
	return rec != nil && rec.MessageID == messageID && rec.RequestID == requestID
}

func claudeRecordsMatchSidechainDedupeKey(rec *fingerprint.ParsedRecord, messageID string, candidateIsSidechain bool) bool {
	return rec != nil && rec.MessageID == messageID && (candidateIsSidechain || rec.IsSidechain)
}

func shouldReplaceClaudeRecord(candidate, existing *fingerprint.ParsedRecord) bool {
	if candidate.IsSidechain != existing.IsSidechain {
		return existing.IsSidechain
	}
	candidateTotal := claudeRecordTotal(candidate)
	existingTotal := claudeRecordTotal(existing)
	if candidateTotal != existingTotal {
		return candidateTotal > existingTotal
	}
	return candidate.UsageSpeed != "" && existing.UsageSpeed == ""
}

func claudeRecordTotal(rec *fingerprint.ParsedRecord) int64 {
	if rec.TotalTokens > 0 {
		return rec.TotalTokens
	}
	return rec.InputTokens + rec.OutputTokens + rec.CacheCreationTokens + rec.CacheReadTokens
}

func addClaudeDedupeIndex(indexes map[string][]int, key string, index int) {
	for _, existing := range indexes[key] {
		if existing == index {
			return
		}
	}
	indexes[key] = append(indexes[key], index)
}

func hasClaudeUnsupportedNullField(line []byte) bool {
	for _, field := range []string{
		"id",
		"cwd",
		"model",
		"speed",
		"costUSD",
		"version",
		"sessionId",
		"requestId",
		"isApiErrorMessage",
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
	} {
		if bytes.Contains(line, []byte(`"`+field+`":null`)) {
			return true
		}
	}
	return false
}

func normalizeClaudeDiscoverPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{"~/.config/claude/projects", "~/.claude/projects"}
	}

	normalized := make([]string, 0, len(paths)+1)
	includeXDGDefault := false
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		expanded := expandHome(path)
		if isLegacyClaudeRoot(path, expanded) {
			includeXDGDefault = true
		}
		projectsDir := filepath.Join(expanded, "projects")
		if info, err := os.Stat(projectsDir); err == nil && info.IsDir() {
			normalized = append(normalized, projectsDir)
			continue
		}
		normalized = append(normalized, path)
	}
	if includeXDGDefault {
		xdgProjects := expandHome("~/.config/claude/projects")
		if info, err := os.Stat(xdgProjects); err == nil && info.IsDir() {
			normalized = append(normalized, xdgProjects)
		}
	}
	return uniqueStrings(normalized)
}

func isLegacyClaudeRoot(raw, expanded string) bool {
	cleanRaw := filepath.ToSlash(strings.TrimSpace(raw))
	cleanExpanded := filepath.Clean(expanded)
	return cleanRaw == "~/.claude" || filepath.Base(cleanExpanded) == ".claude"
}

func optionalFloat64(obj map[string]interface{}, key string) *float64 {
	if obj == nil {
		return nil
	}
	value, ok := obj[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case float64:
		return &v
	case json.Number:
		parsed, err := v.Float64()
		if err == nil {
			return &parsed
		}
	}
	return nil
}

func getBool(obj map[string]interface{}, key string) bool {
	if obj == nil {
		return false
	}
	value, ok := obj[key]
	if !ok {
		return false
	}
	result, ok := value.(bool)
	return ok && result
}

func extractClaudeSession(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			relative := parts[i+1:]
			if len(relative) == 2 {
				return strings.TrimSuffix(relative[1], ".jsonl")
			}
			if len(relative) >= 4 && relative[len(relative)-2] == "subagents" {
				return relative[len(relative)-3]
			}
			if len(relative) >= 3 {
				return relative[len(relative)-2]
			}
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func extractClaudeProject(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			relative := parts[i+1:]
			if len(relative) == 2 {
				return relative[0]
			}
			if len(relative) >= 4 && relative[len(relative)-2] == "subagents" {
				return strings.Join(relative[:len(relative)-3], string(filepath.Separator))
			}
			if len(relative) > 2 {
				return strings.Join(relative[:len(relative)-2], string(filepath.Separator))
			}
			return relative[0]
		}
	}
	return ""
}
