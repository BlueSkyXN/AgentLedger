package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const (
	traeWorkCNSnapshotSchema          = "agentledger.trae-work-cn.usage.v1"
	traeWorkCNRuntimeCollectionSchema = "agentledger.trae-work-cn.runtime.v1"
	traeWorkCNAccountingProfile       = "trae_work_cn_message_usage_v1"
	traeWorkCNRuntimeSource           = "trae-work-cn://runtime"
	traeWorkCNCollectTimeout          = 2 * time.Minute
)

// TraeWorkCNAdapter directly collects explicitly reported per-message usage
// from a running TRAE Work CN renderer. The runtime collector projects a strict
// usage-only shape before data crosses the local debugger boundary.
type TraeWorkCNAdapter struct {
	runtime     traeWorkCNRuntime
	diagnostics []ImportDiagnostic
}

func NewTraeWorkCNAdapter() *TraeWorkCNAdapter {
	return &TraeWorkCNAdapter{runtime: newTraeWorkCNRuntimeCollector()}
}

func newTraeWorkCNAdapterWithRuntime(runtime traeWorkCNRuntime) *TraeWorkCNAdapter {
	return &TraeWorkCNAdapter{runtime: runtime}
}

func (a *TraeWorkCNAdapter) Name() string { return "trae-work-cn" }

func (a *TraeWorkCNAdapter) Discover(paths []string) ([]string, error) {
	return nil, nil
}

func (a *TraeWorkCNAdapter) Probe(paths []string) (DirectSourceProbe, error) {
	if a.runtime == nil {
		return DirectSourceProbe{}, fmt.Errorf("TRAE Work CN runtime collector is unavailable")
	}
	return a.runtime.Probe(paths)
}

func (a *TraeWorkCNAdapter) Collect(paths []string) ([]*fingerprint.ParsedRecord, []string, error) {
	a.diagnostics = nil
	if a.runtime == nil {
		return nil, nil, fmt.Errorf("TRAE Work CN runtime collector is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), traeWorkCNCollectTimeout)
	defer cancel()
	payloads, warnings, err := a.runtime.Collect(ctx, paths)
	if err != nil {
		return nil, warnings, err
	}

	recordsByIdentity := make(map[string]*fingerprint.ParsedRecord)
	parseDiagnostics := newTraeWorkCNParseDiagnostics()
	var sessionsScanned int64
	var messagesScanned int64
	var assistantMessages int64
	var runtimeDuplicates int64
	var skippedMissingIdentity int64
	var skippedInvalidTimestamp int64
	var skippedMissingUsage int64
	var skippedInvalidUsage int64
	for _, payload := range payloads {
		projection, decodeErr := decodeTraeWorkCNRuntimeProjection(payload)
		if decodeErr != nil {
			return nil, warnings, decodeErr
		}
		sessionsScanned += projection.SessionsScanned
		messagesScanned += projection.MessagesScanned
		assistantMessages += projection.AssistantMessages
		runtimeDuplicates += projection.DuplicateMessages
		skippedMissingIdentity += projection.SkippedMissingIdentity
		skippedInvalidTimestamp += projection.SkippedInvalidTimestamp
		skippedMissingUsage += projection.SkippedMissingUsage
		skippedInvalidUsage += projection.SkippedInvalidUsage

		for index, snapshot := range projection.Records {
			sourceLine, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil {
				parseDiagnostics.add(index+1, "sanitized_envelope_encoding_failed")
				continue
			}
			record, reason := traeWorkCNRecordFromSnapshot(snapshot, traeWorkCNRuntimeSource, index+1, sourceLine)
			if reason != "" {
				parseDiagnostics.add(index+1, reason)
				continue
			}
			identity := record.NativeSessionID + "\x00" + record.NativeEventID
			recordsByIdentity[identity] = record
		}
	}

	records := make([]*fingerprint.ParsedRecord, 0, len(recordsByIdentity))
	var totalTokens int64
	for _, record := range recordsByIdentity {
		if record.TotalTokens > math.MaxInt64-totalTokens {
			return nil, warnings, fmt.Errorf("TRAE Work CN runtime token total overflow")
		}
		totalTokens += record.TotalTokens
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].TimestampMs != records[j].TimestampMs {
			return records[i].TimestampMs < records[j].TimestampMs
		}
		if records[i].NativeSessionID != records[j].NativeSessionID {
			return records[i].NativeSessionID < records[j].NativeSessionID
		}
		return records[i].NativeEventID < records[j].NativeEventID
	})

	warnings = append(warnings, parseDiagnostics.warnings()...)
	skippedTotal := skippedMissingIdentity + skippedInvalidTimestamp + skippedInvalidUsage
	if skippedTotal > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"runtime scan rejected %d malformed assistant message(s) (missing_identity=%d, invalid_timestamp=%d, invalid_usage=%d)",
			skippedTotal,
			skippedMissingIdentity,
			skippedInvalidTimestamp,
			skippedInvalidUsage,
		))
	}
	if runtimeDuplicates > 0 {
		warnings = append(warnings, fmt.Sprintf("runtime scan ignored %d duplicate message observation(s)", runtimeDuplicates))
	}

	a.diagnostics = []ImportDiagnostic{
		{Code: "trae_work_cn_sessions_scanned", Unit: ImportDiagnosticUnitCount, Count: sessionsScanned},
		{Code: "trae_work_cn_messages_scanned", Unit: ImportDiagnosticUnitCount, Count: messagesScanned},
		{Code: "trae_work_cn_assistant_messages", Unit: ImportDiagnosticUnitCount, Count: assistantMessages},
		{Code: "trae_work_cn_unmetered_assistant_messages", Unit: ImportDiagnosticUnitCount, Count: skippedMissingUsage},
		{Code: "trae_work_cn_direct_usage", Unit: ImportDiagnosticUnitUsage, Events: int64(len(records)), Tokens: totalTokens},
	}
	return records, warnings, nil
}

func (a *TraeWorkCNAdapter) ImportDiagnostics() []ImportDiagnostic {
	return append([]ImportDiagnostic(nil), a.diagnostics...)
}

type traeWorkCNRuntimeProjection struct {
	Schema                  string                `json:"schema"`
	SessionsScanned         int64                 `json:"sessions_scanned"`
	MessagesScanned         int64                 `json:"messages_scanned"`
	AssistantMessages       int64                 `json:"assistant_messages"`
	DuplicateMessages       int64                 `json:"duplicate_messages"`
	SkippedMissingIdentity  int64                 `json:"skipped_missing_identity"`
	SkippedInvalidTimestamp int64                 `json:"skipped_invalid_timestamp"`
	SkippedMissingUsage     int64                 `json:"skipped_missing_usage"`
	SkippedInvalidUsage     int64                 `json:"skipped_invalid_usage"`
	Records                 []*traeWorkCNSnapshot `json:"records"`
}

func decodeTraeWorkCNRuntimeProjection(payload string) (*traeWorkCNRuntimeProjection, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var projection traeWorkCNRuntimeProjection
	if err := decoder.Decode(&projection); err != nil {
		return nil, fmt.Errorf("invalid TRAE Work CN runtime projection")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid TRAE Work CN runtime projection")
	}
	if projection.Schema != traeWorkCNRuntimeCollectionSchema || projection.SessionsScanned < 0 || projection.MessagesScanned < 0 || projection.AssistantMessages < 0 || projection.DuplicateMessages < 0 || projection.SkippedMissingIdentity < 0 || projection.SkippedInvalidTimestamp < 0 || projection.SkippedMissingUsage < 0 || projection.SkippedInvalidUsage < 0 {
		return nil, fmt.Errorf("invalid TRAE Work CN runtime projection")
	}
	if projection.Records == nil || projection.SessionsScanned > 10_000 || projection.MessagesScanned > 250_000 || projection.DuplicateMessages > 250_000 || projection.AssistantMessages > projection.MessagesScanned || int64(len(projection.Records)) > projection.AssistantMessages || projection.SkippedMissingIdentity > projection.AssistantMessages || projection.SkippedInvalidTimestamp > projection.AssistantMessages || projection.SkippedMissingUsage > projection.AssistantMessages || projection.SkippedInvalidUsage > projection.AssistantMessages {
		return nil, fmt.Errorf("invalid TRAE Work CN runtime projection")
	}
	accountedAssistantMessages := int64(len(projection.Records)) + projection.SkippedMissingIdentity + projection.SkippedInvalidTimestamp + projection.SkippedMissingUsage + projection.SkippedInvalidUsage
	if accountedAssistantMessages != projection.AssistantMessages {
		return nil, fmt.Errorf("invalid TRAE Work CN runtime projection")
	}
	return &projection, nil
}

func (a *TraeWorkCNAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	return nil, fmt.Errorf("TRAE Work CN uses direct runtime collection; file input is unsupported")
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

func traeWorkCNRecordFromSnapshot(snapshot *traeWorkCNSnapshot, path string, lineNumber int, sourceLine []byte) (*fingerprint.ParsedRecord, string) {
	if snapshot == nil || snapshot.Schema != traeWorkCNSnapshotSchema || snapshot.TokenUsage == nil {
		return nil, "missing_required_fields"
	}
	sessionID := strings.TrimSpace(snapshot.SessionID)
	messageID := strings.TrimSpace(snapshot.MessageID)
	if sessionID == "" || messageID == "" || snapshot.TimestampMs == nil || snapshot.TokenUsage.TotalTokens == nil {
		return nil, "missing_required_fields"
	}
	if len(sessionID) > 512 || len(messageID) > 512 {
		return nil, "invalid_identity"
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

	modelRaw := safeTraeWorkCNModel(snapshot.Model)
	modelNormalized := modelRaw
	modelResolution := model.ModelResolutionDirectEvent
	modelIsFallback := false
	if strings.EqualFold(modelRaw, "auto") || strings.EqualFold(modelRaw, "unknown") {
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

func safeTraeWorkCNModel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 128 {
		return "unknown"
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		alphaNumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		if alphaNumeric || (index > 0 && strings.ContainsRune("._:/+-", rune(char))) {
			continue
		}
		return "unknown"
	}
	return value
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
	warning := fmt.Sprintf("skipped %d invalid TRAE Work CN usage record(s) (%s); samples: %s", d.total, strings.Join(counts, ", "), strings.Join(d.samples, ", "))
	if d.total > len(d.samples) {
		warning += fmt.Sprintf(", ... %d more", d.total-len(d.samples))
	}
	return []string{warning}
}
