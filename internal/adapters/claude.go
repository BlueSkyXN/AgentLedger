package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *ClaudeAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var records []*fingerprint.ParsedRecord
	scanner := newJSONLLineReader(f)
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
		nativeSessionID := strings.TrimSpace(candidate.sessionID)
		sessionPathID := stableSessionPathID(path, "projects")
		modelName := normalizeClaudeModel(candidate.model, getString(candidate.usage, "speed"))
		totalTokens := claudeUsageTotal(candidate.usage)
		if candidate.model == "<synthetic>" && totalTokens == 0 {
			continue
		}
		messageID := candidate.messageID
		if messageID == "" {
			messageID = candidate.uuid
		}
		// Claude's durable native identity is message.id (uuid is its parser
		// fallback) followed by requestId; uuid is not promoted to a separate
		// event strategy because one message can be replayed across segments.
		nativeEventID := strings.TrimSpace(messageID)
		identityKind := "message"
		if nativeEventID == "" && candidate.requestID != "" {
			nativeEventID, identityKind = candidate.requestID, "request"
		}

		rec := &fingerprint.ParsedRecord{
			Agent:                 "claude",
			Provider:              "anthropic",
			Model:                 modelName,
			NativeSessionID:       nativeSessionID,
			SessionPathID:         sessionPathID,
			NativeEventID:         nativeEventID,
			IdentityKind:          identityKind,
			IdentityScope:         "session",
			IdentitySubkey:        claudeDedupeID(messageID, candidate.requestID),
			ParserVersion:         "claude-v2",
			Granularity:           "request",
			TimestampMs:           parseTimestamp(candidate.timestamp),
			SessionID:             sessionID,
			ProjectPath:           firstNonEmpty(getString(obj, "cwd"), extractClaudeProject(path)),
			MessageID:             messageID,
			RequestID:             candidate.requestID,
			InputTokens:           getInt64(candidate.usage, "input_tokens"),
			OutputTokens:          getInt64(candidate.usage, "output_tokens"),
			CacheCreationTokens:   getInt64(candidate.usage, "cache_creation_input_tokens"),
			CacheReadTokens:       getInt64(candidate.usage, "cache_read_input_tokens"),
			CacheCreation1hTokens: claudeCacheCreation1hTokens(candidate.usage),
			IsSidechain:           candidate.isSidechain,
			UsageSpeed:            getString(candidate.usage, "speed"),
			SourceProduct:         "claude-code",
			TokenAccountingMethod: model.AccClaudeUsageSum,
			AccountingProfile:     "claude_usage_v1",
			// TotalTokens left as 0 — import.go computes the full sum (incl. cache) consistently with other adapters
			FingerprintJSON: string(rawJSON),
			SourceFile:      path,
			LineNumber:      lineNum,
			RawSHA256:       rawHash,
		}

		if rec.RequestID == "" && rec.NativeEventID != "" {
			rec.IdentitySubkey = claudeRequestlessSubkey(rec.MessageID, rec.TimestampMs)
		}
		records = append(records, rec)
		iterations, _ := candidate.usage["iterations"].([]interface{})
		for index, value := range iterations {
			usage, ok := value.(map[string]interface{})
			if !ok || getString(usage, "type") != "advisor_message" || claudeUsageTotal(usage) == 0 {
				continue
			}
			advisor := *rec
			advisor.UsageSegment = "advisor:" + strconv.Itoa(index)
			advisor.Model = normalizeClaudeModel(getString(usage, "model"), getString(usage, "speed"))
			advisor.UsageSpeed = getString(usage, "speed")
			advisor.InputTokens = getInt64(usage, "input_tokens")
			advisor.OutputTokens = getInt64(usage, "output_tokens")
			advisor.CacheReadTokens = getInt64(usage, "cache_read_input_tokens")
			advisor.CacheCreationTokens = getInt64(usage, "cache_creation_input_tokens")
			advisor.CacheCreation1hTokens = claudeCacheCreation1hTokens(usage)
			if advisor.NativeEventID == "" {
				advisor.IdentityKind = "record"
				advisor.IdentitySubkey = stableRecordIdentitySubkey(lineNum, advisor.UsageSegment)
			} else {
				advisor.IdentitySubkey = claudeTuple(advisor.IdentitySubkey, advisor.UsageSegment)
			}
			records = append(records, &advisor)
		}
	}

	return records, oversizedLineWarnings(scanner.SkippedLines()), scanner.Err()
}

type claudeRecordKey struct {
	message, request, session, segment string
	timestamp                          int64
}

func (a *ClaudeAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	deduped := make([]*fingerprint.ParsedRecord, 0, len(records))
	exact := make(map[claudeRecordKey]int, len(records))
	routes := make(map[claudeRecordKey][]int)
	sidechains := make(map[claudeRecordKey][]int)
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if rec.MessageID == "" {
			deduped = append(deduped, rec)
			continue
		}
		key := claudeRecordKey{message: rec.MessageID, request: rec.RequestID, segment: rec.UsageSegment}
		session := firstNonEmpty(rec.NativeSessionID, rec.SessionID, rec.SessionPathID)
		if rec.RequestID == "" {
			key.session, key.timestamp = session, rec.TimestampMs
		}
		route := claudeRecordKey{message: rec.MessageID, session: session, segment: rec.UsageSegment}
		index, found := exact[key]
		if !found {
			candidates := sidechains[route]
			if rec.IsSidechain {
				candidates = routes[route]
			}
			for _, candidate := range candidates {
				existing := deduped[candidate]
				if rec.IsSidechain || existing.IsSidechain {
					index, found = candidate, true
					break
				}
			}
		}
		if found {
			if shouldReplaceClaudeRecord(rec, deduped[index]) {
				deduped[index] = rec
			}
		} else {
			index = len(deduped)
			deduped = append(deduped, rec)
		}
		exact[key] = index
		if !found {
			routes[route] = append(routes[route], index)
			if rec.IsSidechain {
				sidechains[route] = append(sidechains[route], index)
			}
		} else {
			routes[route] = appendClaudeIndex(routes[route], index)
			if rec.IsSidechain {
				sidechains[route] = appendClaudeIndex(sidechains[route], index)
			}
		}
	}
	return deduped
}

func appendClaudeIndex(indexes []int, index int) []int {
	for _, existing := range indexes {
		if existing == index {
			return indexes
		}
	}
	return append(indexes, index)
}

func claudeTuple(parts ...string) string {
	encoded, _ := json.Marshal(parts)
	return string(encoded)
}

func claudeRequestlessSubkey(messageID string, timestamp int64) string {
	// Some gateways reuse message IDs; the source timestamp distinguishes their responses.
	return claudeTuple("requestless", messageID, strconv.FormatInt(timestamp, 10))
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
		sessionID:   firstNonEmpty(getString(wrapper, "sessionId"), getString(obj, "sessionId")),
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

// claudeCacheCreation1hTokens returns the 1-hour TTL share of
// cache_creation_input_tokens from usage.cache_creation. It returns nil when
// the source carries no TTL split, so pricing falls back to the profile
// assumption instead of treating unknown writes as 5-minute writes.
func claudeCacheCreation1hTokens(usage map[string]interface{}) *int64 {
	split := getMap(usage, "cache_creation")
	if split == nil {
		return nil
	}
	_, has1h := split["ephemeral_1h_input_tokens"]
	_, has5m := split["ephemeral_5m_input_tokens"]
	if !has1h && !has5m {
		return nil
	}
	oneHour := getInt64(split, "ephemeral_1h_input_tokens")
	if oneHour < 0 {
		oneHour = 0
	}
	// The split is a breakdown of the canonical total; never let it exceed it.
	if total := getInt64(usage, "cache_creation_input_tokens"); oneHour > total {
		oneHour = total
	}
	return &oneHour
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
