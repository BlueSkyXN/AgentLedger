package usageevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactClaudeAllowlistAndIdentityEvidence(t *testing.T) {
	raw := `{
        "type":"assistant",
        "uuid":"uuid-fallback",
        "cwd":"/private/project",
        "content":"secret assistant content",
        "thinking":"private reasoning",
        "git":{"remote":"https://secret.example"},
        "team":{"name":"private"},
        "isSidechain":true,
        "costUSD":1.25,
        "message":{"id":"message-1","content":"secret message","usage":{"input_tokens":10,"output_tokens":5,"speed":"fast"}}
    }`

	result := Compact("claude", raw)
	if result.Status != StatusRecognizedLegacy || result.Err != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(result.JSON, "secret") || strings.Contains(result.JSON, "private") || strings.Contains(result.JSON, "cwd") || strings.Contains(result.JSON, "thinking") || strings.Contains(result.JSON, "git") || strings.Contains(result.JSON, "team") {
		t.Fatalf("compact evidence leaked source data: %s", result.JSON)
	}
	evidence := decodeEvidence(t, result.JSON)
	if evidence["schema"] != ClaudeSchema || evidence["source_variant"] != "assistant_message" || evidence["is_sidechain"] != true || evidence["message_id_source"] != "message_id" {
		t.Fatalf("unexpected Claude evidence: %#v", evidence)
	}
	if evidence["cost_usd"] != json.Number("1.25") {
		t.Fatalf("cost evidence missing or changed: %#v", evidence)
	}
	usage := evidenceObject(t, evidence, "usage")
	if usage["input_tokens"] != json.Number("10") || usage["output_tokens"] != json.Number("5") || usage["speed"] != "fast" {
		t.Fatalf("usage map was not preserved: %#v", usage)
	}
}

func TestCompactClaudeWrappedAndUUIDFallback(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		variant        string
		identitySource string
		wantCost       bool
	}{
		{
			name:    "wrapped",
			raw:     `{"data":{"message":{"requestId":"request-1","isSidechain":true,"costUSD":2,"message":{"id":"message-1","model":"claude","usage":{"input_tokens":3,"output_tokens":2}}}}}`,
			variant: "agent_progress_wrapped", identitySource: "message_id", wantCost: true,
		},
		{
			name:    "uuid fallback without cost presence",
			raw:     `{"uuid":"uuid-1","message":{"model":"claude","usage":{"input_tokens":3,"output_tokens":2}}}`,
			variant: "assistant_message", identitySource: "uuid_fallback", wantCost: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Compact("claude", tc.raw)
			if result.Status != StatusRecognizedLegacy || result.Err != nil {
				t.Fatalf("unexpected result: %#v", result)
			}
			evidence := decodeEvidence(t, result.JSON)
			if evidence["source_variant"] != tc.variant || evidence["message_id_source"] != tc.identitySource {
				t.Fatalf("unexpected identity evidence: %#v", evidence)
			}
			_, hasCost := evidence["cost_usd"]
			if hasCost != tc.wantCost {
				t.Fatalf("cost presence=%v, want %v: %#v", hasCost, tc.wantCost, evidence)
			}
		})
	}
}

func TestCompactClaudeWithoutMessageIDOrUUIDIsUnknown(t *testing.T) {
	result := Compact("claude", `{"message":{"usage":{"input_tokens":3,"output_tokens":2}}}`)
	if result.Status != StatusUnknown || result.Err != nil || result.JSON != "" {
		t.Fatalf("missing Claude identity must be omitted: %#v", result)
	}
	wrapped := Compact("claude", `{"data":{"message":{"message":{"usage":{"input_tokens":3,"output_tokens":2}}}}}`)
	if wrapped.Status != StatusUnknown || wrapped.Err != nil || wrapped.JSON != "" {
		t.Fatalf("wrapped Claude source without message ID must be omitted: %#v", wrapped)
	}
}

func TestCompactCodexTokenCountPreservesOnlyPresentUsageMaps(t *testing.T) {
	raw := `{
        "type":"event_msg",
        "timestamp":"2026-01-01T00:00:00Z",
        "context":"secret",
        "payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":10}},"info":{
            "total_token_usage":{"input_tokens":80,"cached_input_tokens":10},
            "last_token_usage":{"output_tokens":20,"reasoning_output_tokens":3}
        }}
    }`
	result := Compact("codex", raw)
	if result.Status != StatusRecognizedLegacy || result.Err != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(result.JSON, "timestamp") || strings.Contains(result.JSON, "rate_limits") || strings.Contains(result.JSON, "secret") || strings.Contains(result.JSON, "context") {
		t.Fatalf("compact evidence leaked non-usage data: %s", result.JSON)
	}
	evidence := decodeEvidence(t, result.JSON)
	if evidence["schema"] != CodexSchema || evidence["source_variant"] != "token_count" {
		t.Fatalf("unexpected Codex evidence: %#v", evidence)
	}
	total := evidenceObject(t, evidence, "total_token_usage")
	if len(total) != 2 || total["input_tokens"] != json.Number("80") || total["cached_input_tokens"] != json.Number("10") {
		t.Fatalf("total usage map was changed or padded: %#v", total)
	}
	last := evidenceObject(t, evidence, "last_token_usage")
	if len(last) != 2 || last["output_tokens"] != json.Number("20") || last["reasoning_output_tokens"] != json.Number("3") {
		t.Fatalf("last usage map was changed or padded: %#v", last)
	}
}

func TestCompactCodexTokenCountMapPresence(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantTotal  bool
		wantLast   bool
		wantStatus Status
	}{
		{
			name:      "total only",
			raw:       `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":9}}}}`,
			wantTotal: true, wantStatus: StatusRecognizedLegacy,
		},
		{
			name:     "last only",
			raw:      `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":9}}}}`,
			wantLast: true, wantStatus: StatusRecognizedLegacy,
		},
		{
			name:       "no usage maps",
			raw:        `{"type":"event_msg","payload":{"type":"token_count","info":{}}}`,
			wantStatus: StatusUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Compact("codex", tc.raw)
			if result.Status != tc.wantStatus || result.Err != nil {
				t.Fatalf("unexpected result: %#v", result)
			}
			if tc.wantStatus != StatusRecognizedLegacy {
				return
			}
			evidence := decodeEvidence(t, result.JSON)
			_, hasTotal := evidence["total_token_usage"]
			_, hasLast := evidence["last_token_usage"]
			if hasTotal != tc.wantTotal || hasLast != tc.wantLast {
				t.Fatalf("usage map presence total=%v last=%v evidence=%#v", hasTotal, hasLast, evidence)
			}
		})
	}
}

func TestCompactCodexHeadlessVariants(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		variant string
	}{
		{name: "root", raw: `{"usage":{"input_tokens":1},"wrapper":"secret"}`, variant: "headless_root"},
		{name: "data", raw: `{"data":{"usage":{"input_tokens":2},"content":"secret"}}`, variant: "headless_data"},
		{name: "result", raw: `{"result":{"usage":{"input_tokens":3},"tool_args":"secret"}}`, variant: "headless_result"},
		{name: "response", raw: `{"response":{"usage":{"input_tokens":4},"thinking":"secret"}}`, variant: "headless_response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Compact("codex", tc.raw)
			if result.Status != StatusRecognizedLegacy || result.Err != nil {
				t.Fatalf("unexpected result: %#v", result)
			}
			if strings.Contains(result.JSON, "secret") {
				t.Fatalf("compact headless evidence leaked data: %s", result.JSON)
			}
			evidence := decodeEvidence(t, result.JSON)
			if evidence["source_variant"] != tc.variant {
				t.Fatalf("unexpected variant: %#v", evidence)
			}
			if len(evidenceObject(t, evidence, "usage")) != 1 {
				t.Fatalf("unexpected headless usage: %#v", evidence)
			}
		})
	}
}

func TestCompactStatusAndDeterminism(t *testing.T) {
	compact := `{"usage":{"output_tokens":2,"input_tokens":1},"source_variant":"headless_root","schema":"agentledger.codex-usage.v1"}`
	result := Compact("codex", compact)
	if result.Status != StatusAlreadyCompact || result.Err != nil || !IsCompact("codex", result.JSON) {
		t.Fatalf("already compact result invalid: %#v", result)
	}
	if result.JSON != `{"schema":"agentledger.codex-usage.v1","source_variant":"headless_root","usage":{"input_tokens":1,"output_tokens":2}}` {
		t.Fatalf("compact JSON is not deterministic: %s", result.JSON)
	}
	if got := Compact("claude", " "); got.Status != StatusEmpty || got.Err != nil || got.JSON != "" {
		t.Fatalf("empty result invalid: %#v", got)
	}
	if got := Compact("unknown", `{}`); got.Status != StatusUnknown || got.Err != nil || got.JSON != "" {
		t.Fatalf("unknown result invalid: %#v", got)
	}
	invalid := Compact("codex", `{"usage":`)
	if invalid.Status != StatusUnknown || invalid.Err != nil || invalid.JSON != "" {
		t.Fatalf("invalid source JSON must be unknown: %#v", invalid)
	}
	nonObject := Compact("codex", `[]`)
	if nonObject.Status != StatusUnknown || nonObject.Err != nil || nonObject.JSON != "" {
		t.Fatalf("non-object source JSON must be unknown: %#v", nonObject)
	}
	for _, raw := range []string{
		`{"schema":"agentledger.codex-usage.v1","source_variant":"headless_root","usage":{"input_tokens":1},"content":"raw injection"}`,
		`{"schema":"agentledger.claude-usage.v1","source_variant":"assistant_message","usage":{"input_tokens":1},"is_sidechain":false,"message_id_source":"message_id","tool_args":"raw injection"}`,
		`{"schema":"agentledger.codex-usage.v1","source_variant":"not-a-variant","usage":{"input_tokens":1}}`,
	} {
		channel := "codex"
		if strings.Contains(raw, ClaudeSchema) {
			channel = "claude"
		}
		result := Compact(channel, raw)
		if result.Status != StatusUnknown || result.Err != nil || result.JSON != "" || IsCompact(channel, raw) {
			t.Fatalf("invalid compact marker was accepted: %#v", result)
		}
	}
}

func TestCompactRejectsSensitiveNestedUsageValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		channel string
		raw     string
	}{
		{
			name:    "Claude legacy nested prompt",
			channel: "claude",
			raw:     `{"message":{"id":"message-1","usage":{"input_tokens":1,"prompt":"private prompt"}}}`,
		},
		{
			name:    "Codex legacy nested prompt",
			channel: "codex",
			raw:     `{"usage":{"input_tokens":1,"prompt":"private prompt"}}`,
		},
		{
			name:    "Claude forged compact nested tool args",
			channel: "claude",
			raw:     `{"schema":"agentledger.claude-usage.v1","source_variant":"assistant_message","usage":{"input_tokens":1,"tool_args":{"path":"private"}},"is_sidechain":false,"message_id_source":"message_id"}`,
		},
		{
			name:    "Codex forged compact nested prompt",
			channel: "codex",
			raw:     `{"schema":"agentledger.codex-usage.v1","source_variant":"headless_root","usage":{"input_tokens":1,"prompt":"private prompt"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Compact(tc.channel, tc.raw)
			if result.Status != StatusUnknown || result.JSON != "" || result.Err != nil || IsCompact(tc.channel, tc.raw) {
				t.Fatalf("sensitive nested usage value was accepted: %#v", result)
			}
		})
	}
}

func TestCompactClaudePreservesKnownStructuredUsageEvidence(t *testing.T) {
	raw := `{"message":{"id":"message-1","usage":{"input_tokens":1,"speed":"fast","service_tier":"standard","inference_geo":"us","cache_creation":{"ephemeral_5m_input_tokens":2},"server_tool_use":{"web_search_requests":3},"iterations":[{"type":"model","model":"claude-test","output_tokens":4}]}}}`
	result := Compact("claude", raw)
	if result.Status != StatusRecognizedLegacy || result.Err != nil {
		t.Fatalf("known structured Claude usage was rejected: %#v", result)
	}
	evidence := decodeEvidence(t, result.JSON)
	usage := evidenceObject(t, evidence, "usage")
	if usage["speed"] != "fast" || usage["service_tier"] != "standard" || usage["inference_geo"] != "us" {
		t.Fatalf("known Claude string evidence changed: %#v", usage)
	}
	if _, ok := usage["cache_creation"].(map[string]interface{}); !ok {
		t.Fatalf("cache creation evidence missing: %#v", usage)
	}
	if _, ok := usage["iterations"].([]interface{}); !ok {
		t.Fatalf("iteration evidence missing: %#v", usage)
	}
}

func decodeEvidence(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var evidence map[string]interface{}
	if err := decoder.Decode(&evidence); err != nil {
		t.Fatalf("decode compact evidence: %v", err)
	}
	return evidence
}

func evidenceObject(t *testing.T, evidence map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	value, ok := evidence[key].(map[string]interface{})
	if !ok {
		t.Fatalf("evidence %q is not an object: %#v", key, evidence)
	}
	return value
}
