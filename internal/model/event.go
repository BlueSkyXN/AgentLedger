package model

const (
	AccClaudeUsageSum           = "claude_usage_sum"
	AccCodexLastTokenUsage      = "codex_last_token_usage"
	AccCodexTotalDelta          = "codex_total_delta"
	AccCodexHeadlessUsage       = "codex_headless_usage"
	AccCopilotOtelParts         = "copilot_otel_parts"
	AccCopilotOtelTotalFallback = "copilot_otel_total_fallback"
	AccCopilotSessionMetrics    = "copilot_session_model_metrics"
)

// UsageEvent is the v2 fact-table representation for one local agent usage event.
type UsageEvent struct {
	EventID        string `json:"event_id" db:"event_id"`
	DedupeKey      string `json:"dedupe_key" db:"dedupe_key"`
	DedupeStrategy string `json:"dedupe_strategy" db:"dedupe_strategy"`

	Channel         string `json:"channel" db:"channel"`
	Provider        string `json:"provider" db:"provider"`
	ModelRaw        string `json:"model_raw" db:"model_raw"`
	ModelNormalized string `json:"model_normalized" db:"model_normalized"`

	SourceAgent           string `json:"source_agent" db:"source_agent"`
	SourceProduct         string `json:"source_product" db:"source_product"`
	ObservabilityLevel    string `json:"observability_level" db:"observability_level"`
	ModelIsFallback       bool   `json:"model_is_fallback" db:"model_is_fallback"`
	SourceTotalTokens     *int64 `json:"source_total_tokens" db:"source_total_tokens"`
	RawInputTokens        *int64 `json:"raw_input_tokens" db:"raw_input_tokens"`
	TokenAccountingMethod string `json:"token_accounting_method" db:"token_accounting_method"`
	AccountingProfile     string `json:"accounting_profile" db:"accounting_profile"`

	TimestampMs   int64  `json:"timestamp_ms" db:"timestamp_ms"`
	SessionID     string `json:"session_id" db:"session_id"`
	SessionPathID string `json:"session_path_id" db:"session_path_id"`
	TurnID        string `json:"turn_id" db:"turn_id"`
	ProjectPath   string `json:"project_path" db:"project_path"`
	MessageID     string `json:"message_id" db:"message_id"`
	RequestID     string `json:"request_id" db:"request_id"`
	SourceFile    string `json:"source_file" db:"source_file"`
	LineNumber    int    `json:"line_number" db:"line_number"`
	RawSHA256     string `json:"raw_sha256" db:"raw_sha256"`

	InputTokens         int64 `json:"input_tokens" db:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens" db:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens" db:"reasoning_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens" db:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens" db:"cache_read_tokens"`
	TotalTokens         int64 `json:"total_tokens" db:"total_tokens"`

	RequestStartedAtMs *int64   `json:"request_started_at_ms" db:"request_started_at_ms"`
	FirstTokenAtMs     *int64   `json:"first_token_at_ms" db:"first_token_at_ms"`
	CompletedAtMs      *int64   `json:"completed_at_ms" db:"completed_at_ms"`
	TotalDurationMs    *int64   `json:"total_duration_ms" db:"total_duration_ms"`
	TTFTMs             *int64   `json:"ttft_ms" db:"ttft_ms"`
	OutputDurationMs   *int64   `json:"output_duration_ms" db:"output_duration_ms"`
	OutputTPS          *float64 `json:"output_tps" db:"output_tps"`

	RecordedCostUSD *float64 `json:"recorded_cost_usd" db:"recorded_cost_usd"`
	RawUsageJSON    string   `json:"raw_usage_json" db:"raw_usage_json"`

	ImportedAtMs int64 `json:"imported_at_ms" db:"imported_at_ms"`
	UpdatedAtMs  int64 `json:"updated_at_ms" db:"updated_at_ms"`
}

func (e *UsageEvent) TotalTokensComputed() int64 {
	return e.InputTokens + e.OutputTokens + e.CacheCreationTokens + e.CacheReadTokens + e.ReasoningTokens
}
