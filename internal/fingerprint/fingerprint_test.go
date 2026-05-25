package fingerprint

import "testing"

func TestComputeMessageID(t *testing.T) {
	rec := &ParsedRecord{
		Agent:     "claude",
		Provider:  "anthropic",
		MessageID: "msg_123",
	}
	fp, strategy := Compute(rec)
	if strategy != StrategyMessageID {
		t.Errorf("expected message_id strategy, got %s", strategy)
	}
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestComputeSessionToken(t *testing.T) {
	rec := &ParsedRecord{
		Agent:        "codex",
		Provider:     "openai",
		SessionID:    "sess_abc",
		TimestampMs:  1700000000000,
		InputTokens:  100,
		OutputTokens: 200,
	}
	fp, strategy := Compute(rec)
	if strategy != StrategySessionToken {
		t.Errorf("expected session_token strategy, got %s", strategy)
	}
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestComputeRawHash(t *testing.T) {
	rec := &ParsedRecord{
		Agent:    "gemini",
		Provider: "google",
		RawJSON:  `{"b":2,"a":1}`,
	}
	fp, strategy := Compute(rec)
	if strategy != StrategyRawHash {
		t.Errorf("expected raw_hash strategy, got %s", strategy)
	}
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}

	rec2 := &ParsedRecord{
		Agent:    "gemini",
		Provider: "google",
		RawJSON:  `{"a":1,"b":2}`,
	}
	fp2, _ := Compute(rec2)
	if fp != fp2 {
		t.Errorf("canonicalized JSON should produce same fingerprint: %s vs %s", fp, fp2)
	}
}

func TestComputeFallback(t *testing.T) {
	rec := &ParsedRecord{
		Agent:      "qwen",
		Provider:   "alibaba",
		SourceFile: "/path/to/file.jsonl",
		LineNumber: 42,
		RawSHA256:  "abc123",
	}
	fp, strategy := Compute(rec)
	if strategy != StrategyFallback {
		t.Errorf("expected fallback strategy, got %s", strategy)
	}
	if fp == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestStableJSONCanonicalizes(t *testing.T) {
	result := stableJSON(`{"z":1,"a":2,"m":3}`)
	expected := `{"a":2,"m":3,"z":1}`
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}
