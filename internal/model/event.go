package model

// UsageEvent represents a single AI agent usage event
type UsageEvent struct {
	EventFingerprint    string `json:"event_fingerprint" db:"event_fingerprint"`
	DedupeKey           string `json:"dedupe_key" db:"dedupe_key"`
	FingerprintStrategy string `json:"fingerprint_strategy" db:"fingerprint_strategy"`

	OriginDeviceID    string `json:"origin_device_id" db:"origin_device_id"`
	FirstSeenDeviceID string `json:"first_seen_device_id" db:"first_seen_device_id"`
	LastSeenDeviceID  string `json:"last_seen_device_id" db:"last_seen_device_id"`

	Agent          string `json:"agent" db:"agent"`
	Provider       string `json:"provider" db:"provider"`
	ClientName     string `json:"client_name" db:"client_name"`
	SourceChannel  string `json:"source_channel" db:"source_channel"`
	BillingChannel string `json:"billing_channel" db:"billing_channel"`
	SourceKind     string `json:"source_kind" db:"source_kind"`

	ModelRaw        string `json:"model_raw" db:"model_raw"`
	ModelNormalized string `json:"model_normalized" db:"model_normalized"`
	ModelProvider   string `json:"model_provider" db:"model_provider"`
	ModelFamily     string `json:"model_family" db:"model_family"`
	IsFallbackModel bool   `json:"is_fallback_model" db:"is_fallback_model"`

	SpeedLabel      string  `json:"speed_label" db:"speed_label"`
	ServiceTier     string  `json:"service_tier" db:"service_tier"`
	SpeedMultiplier float64 `json:"speed_multiplier" db:"speed_multiplier"`
	IsFastMode      bool    `json:"is_fast_mode" db:"is_fast_mode"`

	TimestampMs            int64  `json:"timestamp_ms" db:"timestamp_ms"`
	TimestampText          string `json:"timestamp_text" db:"timestamp_text"`
	SourceTimezone         string `json:"source_timezone" db:"source_timezone"`
	TimestampOffsetMinutes int    `json:"timestamp_offset_minutes" db:"timestamp_offset_minutes"`

	SessionID             string `json:"session_id" db:"session_id"`
	ConversationID        string `json:"conversation_id" db:"conversation_id"`
	Project               string `json:"project" db:"project"`
	ProjectPathRaw        string `json:"project_path_raw" db:"project_path_raw"`
	ProjectPathNormalized string `json:"project_path_normalized" db:"project_path_normalized"`
	WorkspaceKey          string `json:"workspace_key" db:"workspace_key"`

	MessageID string `json:"message_id" db:"message_id"`
	RequestID string `json:"request_id" db:"request_id"`

	InputTokens         int64 `json:"input_tokens" db:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens" db:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens" db:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens" db:"cache_read_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens" db:"reasoning_tokens"`
	ToolTokens          int64 `json:"tool_tokens" db:"tool_tokens"`
	ExtraTotalTokens    int64 `json:"extra_total_tokens" db:"extra_total_tokens"`
	SourceTotalTokens   int64 `json:"source_total_tokens" db:"source_total_tokens"`
	TotalTokens         int64 `json:"total_tokens" db:"total_tokens"`

	CostUSD        float64 `json:"cost_usd" db:"cost_usd"`
	CostSource     string  `json:"cost_source" db:"cost_source"`
	PricingSource  string  `json:"pricing_source" db:"pricing_source"`
	PricingVersion string  `json:"pricing_version" db:"pricing_version"`

	Credits      float64 `json:"credits" db:"credits"`
	MessageCount int     `json:"message_count" db:"message_count"`

	RawUsageJSON string `json:"raw_usage_json" db:"raw_usage_json"`
	RawMetaJSON  string `json:"raw_meta_json" db:"raw_meta_json"`
	RawSHA256    string `json:"raw_sha256" db:"raw_sha256"`

	CreatedAtMs int64 `json:"created_at_ms" db:"created_at_ms"`
	UpdatedAtMs int64 `json:"updated_at_ms" db:"updated_at_ms"`
}

// TotalTokensComputed computes total tokens from components
func (e *UsageEvent) TotalTokensComputed() int64 {
	return e.InputTokens + e.OutputTokens + e.CacheCreationTokens + e.CacheReadTokens + e.ReasoningTokens + e.ToolTokens + e.ExtraTotalTokens
}
