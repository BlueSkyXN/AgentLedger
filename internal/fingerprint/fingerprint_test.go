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

func TestComputeDedupeIDTakesPriorityOverMessageID(t *testing.T) {
	rec := &ParsedRecord{
		Agent:     "claude",
		Provider:  "anthropic",
		DedupeID:  "msg_123:req_1",
		MessageID: "msg_123",
	}
	rec2 := *rec
	rec2.DedupeID = "msg_123:req_2"

	fp, strategy := Compute(rec)
	fp2, strategy2 := Compute(&rec2)
	if strategy != StrategyMessageID || strategy2 != StrategyMessageID {
		t.Fatalf("expected message_id strategy, got %s and %s", strategy, strategy2)
	}
	if fp == fp2 {
		t.Fatal("different dedupe ids should produce distinct fingerprints")
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

func TestComputeSessionTokenIncludesTokenDetails(t *testing.T) {
	sourceTotalA := int64(300)
	sourceTotalB := int64(400)
	base := &ParsedRecord{
		Agent:             "codex",
		Provider:          "openai",
		Model:             "gpt-5",
		SessionID:         "sess_abc",
		TimestampMs:       1700000000000,
		InputTokens:       100,
		OutputTokens:      200,
		TotalTokens:       300,
		SourceTotalTokens: &sourceTotalA,
	}
	withDifferentSourceTotal := *base
	withDifferentSourceTotal.SourceTotalTokens = &sourceTotalB
	withDifferentReasoning := *base
	withDifferentReasoning.ReasoningTokens = 1

	fpA, strategy := Compute(base)
	fpB, _ := Compute(&withDifferentSourceTotal)
	fpC, _ := Compute(&withDifferentReasoning)
	if strategy != StrategySessionToken {
		t.Fatalf("expected session_token strategy, got %s", strategy)
	}
	if fpA == fpB {
		t.Fatal("source total changes should produce a distinct session_token fingerprint")
	}
	if fpA == fpC {
		t.Fatal("reasoning token changes should produce a distinct session_token fingerprint")
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
