package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const (
	traeWorkCNSnapshotSchema    = "agentledger.trae-work-cn.usage.v1"
	traeWorkCNAccountingProfile = "trae_work_cn_message_usage_v1"
	traeWorkCNDefaultPath       = "~/.local/share/agent-ledger/sources/trae-work-cn"
)

// TraeWorkCNAdapter imports explicitly sanitized, per-message usage snapshots.
// TRAE Work CN's native database is encrypted and its current in-process IPC is
// not a stable external interface, so this adapter never reads either directly.
type TraeWorkCNAdapter struct{}

func NewTraeWorkCNAdapter() *TraeWorkCNAdapter { return &TraeWorkCNAdapter{} }

func (a *TraeWorkCNAdapter) Name() string { return "trae-work-cn" }

func (a *TraeWorkCNAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{traeWorkCNDefaultPath}
	}
	files, err := DiscoverFiles(paths, []string{".jsonl"})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (a *TraeWorkCNAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *TraeWorkCNAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open TRAE Work CN snapshot %s: %w", path, err)
	}
	defer f.Close()

	records := make([]*fingerprint.ParsedRecord, 0)
	diagnostics := newTraeWorkCNParseDiagnostics()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		snapshot, reason := decodeTraeWorkCNSnapshot(line)
		if reason != "" {
			diagnostics.add(lineNumber, reason)
			continue
		}
		record, reason := traeWorkCNRecordFromSnapshot(snapshot, path, lineNumber, line)
		if reason != "" {
			diagnostics.add(lineNumber, reason)
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, diagnostics.warnings(), fmt.Errorf("scan TRAE Work CN snapshot %s: %w", path, err)
	}
	return records, diagnostics.warnings(), nil
}

type traeWorkCNSnapshot struct {
	Schema      string                `json:"schema"`
	SessionID   string                `json:"session_id"`
	MessageID   string                `json:"message_id"`
	TimestampMs *json.Number          `json:"timestamp_ms"`
	Model       string                `json:"model"`
	Mode        string                `json:"mode"`
	AgentType   string                `json:"agent_type"`
	TokenUsage  *traeWorkCNTokenUsage `json:"token_usage"`
}

type traeWorkCNTokenUsage struct {
	PromptTokens             *json.Number `json:"prompt_tokens"`
	CompletionTokens         *json.Number `json:"completion_tokens"`
	TotalTokens              *json.Number `json:"total_tokens"`
	CacheCreationInputTokens *json.Number `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *json.Number `json:"cache_read_input_tokens"`
	ReasoningTokens          *json.Number `json:"reasoning_tokens"`
	PromptTokensTotal        *json.Number `json:"prompt_tokens_total"`
	CompletionTokensTotal    *json.Number `json:"completion_tokens_total"`
	LastTurnTotalTokens      *json.Number `json:"last_turn_total_tokens"`
	MaxTokens                *json.Number `json:"max_tokens"`
}

func decodeTraeWorkCNSnapshot(line []byte) (*traeWorkCNSnapshot, string) {
	if !json.Valid(line) {
		return nil, "invalid_json"
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var snapshot traeWorkCNSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, "invalid_schema"
	}
	return &snapshot, ""
}

func traeWorkCNRecordFromSnapshot(snapshot *traeWorkCNSnapshot, path string, lineNumber int, sourceLine []byte) (*fingerprint.ParsedRecord, string) {
	if snapshot == nil || snapshot.Schema != traeWorkCNSnapshotSchema || snapshot.TokenUsage == nil {
		return nil, "missing_required_fields"
	}
	sessionID := strings.TrimSpace(snapshot.SessionID)
	messageID := strings.TrimSpace(snapshot.MessageID)
	if sessionID == "" || messageID == "" || snapshot.TimestampMs == nil || snapshot.TokenUsage.TotalTokens == nil {
		return nil, "missing_required_fields"
	}
	timestampMs, err := snapshot.TimestampMs.Int64()
	if err != nil || timestampMs <= 0 {
		return nil, "invalid_timestamp"
	}

	mode := strings.TrimSpace(snapshot.Mode)
	if !validTraeWorkCNMode(mode) {
		return nil, "invalid_mode"
	}
	agentType := strings.TrimSpace(snapshot.AgentType)
	if !validTraeWorkCNAgentType(agentType) {
		return nil, "invalid_agent_type"
	}

	values := make(map[string]int64)
	present := make(map[string]bool)
	for _, field := range []struct {
		name   string
		number *json.Number
	}{
		{"prompt_tokens", snapshot.TokenUsage.PromptTokens},
		{"completion_tokens", snapshot.TokenUsage.CompletionTokens},
		{"total_tokens", snapshot.TokenUsage.TotalTokens},
		{"cache_creation_input_tokens", snapshot.TokenUsage.CacheCreationInputTokens},
		{"cache_read_input_tokens", snapshot.TokenUsage.CacheReadInputTokens},
		{"reasoning_tokens", snapshot.TokenUsage.ReasoningTokens},
		{"prompt_tokens_total", snapshot.TokenUsage.PromptTokensTotal},
		{"completion_tokens_total", snapshot.TokenUsage.CompletionTokensTotal},
		{"last_turn_total_tokens", snapshot.TokenUsage.LastTurnTotalTokens},
		{"max_tokens", snapshot.TokenUsage.MaxTokens},
	} {
		if field.number == nil {
			continue
		}
		value, err := field.number.Int64()
		if err != nil || value < 0 {
			return nil, "invalid_token_value"
		}
		values[field.name] = value
		present[field.name] = true
	}

	total := values["total_tokens"]
	prompt := values["prompt_tokens"]
	completion := values["completion_tokens"]
	if total <= 0 || prompt > total || completion > total-prompt {
		return nil, "invalid_token_totals"
	}

	modelRaw := strings.TrimSpace(snapshot.Model)
	modelNormalized := modelRaw
	modelResolution := model.ModelResolutionDirectEvent
	modelIsFallback := false
	if modelRaw == "" {
		modelRaw = "unknown"
		modelNormalized = "unknown"
		modelResolution = model.ModelResolutionUnknown
		modelIsFallback = true
	} else if strings.EqualFold(modelRaw, "auto") || strings.EqualFold(modelRaw, "unknown") {
		modelNormalized = "unknown"
		modelResolution = model.ModelResolutionUnknown
		modelIsFallback = true
	}

	envelopeTokenUsage := make(map[string]int64)
	for name, value := range values {
		if present[name] {
			envelopeTokenUsage[name] = value
		}
	}
	envelope := map[string]interface{}{
		"schema":      snapshot.Schema,
		"model":       modelRaw,
		"token_usage": envelopeTokenUsage,
	}
	if mode != "" {
		envelope["mode"] = mode
	}
	if agentType != "" {
		envelope["agent_type"] = agentType
	}
	rawJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, "sanitized_envelope_encoding_failed"
	}

	var rawInputTokens *int64
	if present["prompt_tokens"] {
		rawInputTokens = int64Ptr(prompt)
	}
	return &fingerprint.ParsedRecord{
		Agent:                 "trae-work-cn",
		Provider:              "unknown",
		Model:                 modelRaw,
		ModelNormalized:       modelNormalized,
		ModelResolution:       modelResolution,
		ModelIsFallback:       modelIsFallback,
		TimestampMs:           timestampMs,
		NativeSessionID:       sessionID,
		NativeEventID:         messageID,
		IdentityKind:          "message",
		IdentityScope:         "session",
		ParserVersion:         "trae-work-cn-v1",
		Granularity:           "message",
		SessionID:             sessionID,
		MessageID:             messageID,
		InputTokens:           prompt,
		OutputTokens:          completion,
		TotalTokens:           total,
		SourceTotalTokens:     int64Ptr(total),
		RawInputTokens:        rawInputTokens,
		SourceProduct:         "trae-work-cn",
		ObservabilityLevel:    "partial",
		TokenAccountingMethod: model.AccTraeWorkCNMessageUsage,
		AccountingProfile:     traeWorkCNAccountingProfile,
		FingerprintJSON:       string(rawJSON),
		SourceFile:            path,
		LineNumber:            lineNumber,
		RawSHA256:             sha256Hex(sourceLine),
	}, ""
}

func validTraeWorkCNMode(value string) bool {
	switch value {
	case "", "work", "code", "design":
		return true
	default:
		return false
	}
}

func validTraeWorkCNAgentType(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 || !strings.HasPrefix(value, "solo_") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

type traeWorkCNParseDiagnostics struct {
	total   int
	counts  map[string]int
	samples []string
}

func newTraeWorkCNParseDiagnostics() *traeWorkCNParseDiagnostics {
	return &traeWorkCNParseDiagnostics{counts: make(map[string]int)}
}

func (d *traeWorkCNParseDiagnostics) add(lineNumber int, reason string) {
	d.total++
	d.counts[reason]++
	if len(d.samples) < 5 {
		d.samples = append(d.samples, fmt.Sprintf("line %d %s", lineNumber, reason))
	}
}

func (d *traeWorkCNParseDiagnostics) warnings() []string {
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
	warning := fmt.Sprintf("skipped %d invalid TRAE Work CN snapshot line(s) (%s); samples: %s", d.total, strings.Join(counts, ", "), strings.Join(d.samples, ", "))
	if d.total > len(d.samples) {
		warning += fmt.Sprintf(", ... %d more", d.total-len(d.samples))
	}
	return []string{warning}
}
