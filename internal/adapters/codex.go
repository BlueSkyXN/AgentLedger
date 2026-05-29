package adapters

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const (
	// CodexDuplicatePolicyLedger 是默认、最准确的口径：对累计值 total_token_usage 做
	// per-session 望远镜 delta，跳过冗余重发（delta=0）并整段计入 counter reset。
	CodexDuplicatePolicyLedger = "ledger"
	// CodexDuplicatePolicyCCUsageCompatible 复刻 ccusage 口径：直接用 last_token_usage，
	// 靠含时间戳的 fingerprint 去重，便于与 `ccusage codex` 逐数字交叉核对（会继承其高估）。
	CodexDuplicatePolicyCCUsageCompatible = "ccusage_compatible"
)

type CodexAdapter struct {
	duplicatePolicy string
}

type CodexOptions struct {
	DuplicatePolicy string
}

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
	return NewCodexAdapterWithOptions(CodexOptions{})
}

func NewCodexAdapterWithOptions(options CodexOptions) *CodexAdapter {
	return &CodexAdapter{duplicatePolicy: normalizeCodexDuplicatePolicy(options.DuplicatePolicy)}
}

func (a *CodexAdapter) Name() string { return "codex" }

func normalizeCodexDuplicatePolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case "", CodexDuplicatePolicyLedger:
		return CodexDuplicatePolicyLedger
	case CodexDuplicatePolicyCCUsageCompatible:
		return CodexDuplicatePolicyCCUsageCompatible
	default:
		return CodexDuplicatePolicyLedger
	}
}

func (a *CodexAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.codex/sessions"}
	}
	return DiscoverFiles(normalizeCodexDiscoverPaths(paths), []string{".jsonl"})
}

func normalizeCodexDiscoverPaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range paths {
		path := expandHome(raw)
		base := filepath.Base(filepath.Clean(path))
		if base != "sessions" && base != "archived_sessions" {
			sessions := filepath.Join(path, "sessions")
			if info, err := os.Stat(sessions); err == nil && info.IsDir() {
				path = sessions
			}
		}
		if !seen[path] {
			seen[path] = true
			normalized = append(normalized, path)
		}
	}
	return normalized
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
	sessionPathID := extractCodexSessionPathID(path)
	currentModel := ""
	currentModelIsFallback := false
	previousTotals := map[string]codexUsageSnapshot{}
	lastUsageRecords := map[string]*fingerprint.ParsedRecord{}

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
		if attachCodexTaskTiming(obj, defaultSessionID, lastUsageRecords) {
			continue
		}

		usage, method, sourceUsage, ok, sourceCumulative := extractCodexUsage(obj, entryType)
		if !ok {
			continue
		}

		sessionID := extractCodexSessionID(obj, defaultSessionID)
		// codex 每次真实调用后会冗余重发一条相同的 token_count（累计 total 不变），
		// 且 last_token_usage 仅记最后一次调用、会漏掉同一区间内的多次调用。
		// 默认据此基于权威累计值 total_token_usage 做 per-session 望远镜 delta：
		// 累计不变 → delta=0 自动跳过冗余；累计回落（compact 重置）→ 整段计入避免丢量。
		// ccusage_compatible 保留 ccusage 口径：直接用 last_token_usage，靠含时间戳的
		// fingerprint 去重（会继承 ccusage 对冗余重发的重复计算与对多次调用的漏计）。
		if sourceCumulative {
			if a.duplicatePolicy == CodexDuplicatePolicyCCUsageCompatible {
				method = model.AccCodexLastTokenUsage
				previousTotals[sessionID] = sourceUsage
			} else {
				usage = sourceUsage.telescopingDelta(previousTotals[sessionID])
				method = model.AccCodexTotalDelta
				previousTotals[sessionID] = sourceUsage
			}
		}
		rawInputTokens := usage.inputPtr()
		storedUsage := usage.storageUsage()
		if storedUsage.isZero() {
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
		totalTokens := storedUsage.totalTokens()
		rec := &fingerprint.ParsedRecord{
			Agent:                 "codex",
			Provider:              "openai",
			Model:                 modelName,
			TimestampMs:           extractCodexTimestamp(obj),
			SessionID:             sessionID,
			MessageID:             firstNonEmpty(getString(obj, "id"), getString(obj, "message_id")),
			RequestID:             firstNonEmpty(getString(obj, "request_id"), getNestedString(obj, "payload", "request_id")),
			InputTokens:           storedUsage.Input,
			OutputTokens:          storedUsage.Output,
			CacheReadTokens:       storedUsage.CachedInput,
			ReasoningTokens:       storedUsage.Reasoning,
			TotalTokens:           totalTokens,
			SourceTotalTokens:     sourceUsage.sourceTotalPtr(),
			RawInputTokens:        rawInputTokens,
			ObservabilityLevel:    "full",
			ModelIsFallback:       modelIsFallback,
			TokenAccountingMethod: method,
			AccountingProfile:     a.duplicatePolicy,
			SessionPathID:         sessionPathID,
			RawJSON:               string(rawJSON),
			SourceFile:            path,
			LineNumber:            lineNum,
			RawSHA256:             rawHash,
		}
		records = append(records, rec)
		lastUsageRecords[sessionID] = rec
		lastUsageRecords[""] = rec
	}

	return records, scanner.Err()
}

// extractCodexUsage 返回 (usage, method, source, ok, sourceCumulative)。
// sourceCumulative 为 true 表示 source 来自累计字段 total_token_usage，
// 调用方据此做望远镜 delta；为 false 表示只有单次样本（last_token_usage 或 headless usage）。
func extractCodexUsage(obj map[string]interface{}, entryType string) (codexUsageSnapshot, string, codexUsageSnapshot, bool, bool) {
	if entryType == "event_msg" {
		payload := getMap(obj, "payload")
		if payload == nil || getString(payload, "type") != "token_count" {
			return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false, false
		}
		info := getMap(payload, "info")
		if info == nil {
			return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false, false
		}
		// 优先使用累计值 total_token_usage（权威、可做 per-session 望远镜 delta）。
		if totalUsage := getMap(info, "total_token_usage"); totalUsage != nil {
			cumulative := codexUsageFromMap(totalUsage)
			last := cumulative
			if lastUsage := getMap(info, "last_token_usage"); lastUsage != nil {
				last = codexUsageFromMap(lastUsage)
			}
			return last, model.AccCodexLastTokenUsage, cumulative, true, true
		}
		// 仅有单次 last_token_usage（无累计）：作为非累计样本直接使用。
		if lastUsage := getMap(info, "last_token_usage"); lastUsage != nil {
			snapshot := codexUsageFromMap(lastUsage)
			return snapshot, model.AccCodexLastTokenUsage, snapshot, true, false
		}
		return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false, false
	}

	if entryType != "" && !hasHeadlessCodexUsage(obj) {
		return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false, false
	}
	for _, container := range []map[string]interface{}{obj, getMap(obj, "data"), getMap(obj, "result"), getMap(obj, "response")} {
		if container == nil {
			continue
		}
		if usage := getMap(container, "usage"); usage != nil {
			snapshot := codexUsageFromMap(usage)
			return snapshot, model.AccCodexHeadlessUsage, snapshot, true, false
		}
	}
	return codexUsageSnapshot{}, "", codexUsageSnapshot{}, false, false
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

// telescopingDelta 用累计值还原单步真实增量。previous 为空（本段首条）时直接取
// current；current.Total < previous.Total 视为累计计数器重置（如 compact 压缩上下文），
// 整段计入以避免丢量；其余情况按分量做饱和差。
func (current codexUsageSnapshot) telescopingDelta(previous codexUsageSnapshot) codexUsageSnapshot {
	if !previous.HasTotal || current.Total >= previous.Total {
		return current.delta(previous)
	}
	return current
}

func (u codexUsageSnapshot) isZero() bool {
	return u.Input == 0 && u.CachedInput == 0 && u.Output == 0 && u.Reasoning == 0
}

func (u codexUsageSnapshot) storageUsage() codexUsageSnapshot {
	cached := minInt64(u.CachedInput, u.Input)
	u.Input = saturatingSub(u.Input, cached)
	u.CachedInput = cached
	return u
}

func (u codexUsageSnapshot) totalTokens() int64 {
	if u.HasTotal {
		return u.Total
	}
	return u.Input + u.CachedInput + maxInt64(u.Output, u.Reasoning)
}

func (u codexUsageSnapshot) sourceTotalPtr() *int64 {
	if !u.HasTotal {
		return nil
	}
	return int64Ptr(u.Total)
}

func (u codexUsageSnapshot) inputPtr() *int64 {
	if !u.HasInput {
		return nil
	}
	return int64Ptr(u.Input)
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

func attachCodexTaskTiming(obj map[string]interface{}, fallbackSessionID string, lastUsageRecords map[string]*fingerprint.ParsedRecord) bool {
	if getString(obj, "type") != "event_msg" {
		return false
	}
	payload := getMap(obj, "payload")
	if payload == nil || getString(payload, "type") != "task_complete" {
		return false
	}
	sessionID := extractCodexSessionID(obj, fallbackSessionID)
	rec := lastUsageRecords[sessionID]
	if rec == nil {
		rec = lastUsageRecords[""]
	}
	if rec == nil {
		return true
	}

	durationMs := getInt64(payload, "duration_ms")
	ttftMs := getInt64(payload, "time_to_first_token_ms")
	completedAtMs := parseTimestamp(payload["completed_at"])
	if completedAtMs == 0 {
		completedAtMs = extractCodexTimestamp(obj)
	}

	if rec.TotalDurationMs == 0 && durationMs > 0 {
		rec.TotalDurationMs = durationMs
	}
	if rec.TTFTMs == 0 && ttftMs > 0 {
		rec.TTFTMs = ttftMs
	}
	if rec.CompletedAtMs == 0 && completedAtMs > 0 {
		rec.CompletedAtMs = completedAtMs
	}
	if rec.RequestStartedAtMs == 0 && completedAtMs > 0 && durationMs > 0 {
		rec.RequestStartedAtMs = completedAtMs - durationMs
	}
	if rec.FirstTokenAtMs == 0 && rec.RequestStartedAtMs > 0 && ttftMs > 0 {
		rec.FirstTokenAtMs = rec.RequestStartedAtMs + ttftMs
	}
	if rec.TurnID == "" {
		rec.TurnID = getString(payload, "turn_id")
	}
	return true
}

func extractCodexSession(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func extractCodexSessionPathID(path string) string {
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for i, part := range parts {
		if part != "sessions" && part != "archived_sessions" {
			continue
		}
		if i+1 >= len(parts) {
			break
		}
		rel := filepath.Join(parts[i+1:]...)
		rel = strings.TrimSuffix(rel, filepath.Ext(rel))
		if part == "archived_sessions" {
			return filepath.ToSlash(filepath.Join("archived_sessions", rel))
		}
		return filepath.ToSlash(rel)
	}
	return extractCodexSession(path)
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

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
