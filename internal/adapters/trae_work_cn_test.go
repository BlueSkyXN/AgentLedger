package adapters

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestTraeWorkCNAdapterParsesSanitizedMessageUsage(t *testing.T) {
	records, warnings := parseSyntheticTraeWorkCNUsageRecords([]string{`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"synthetic-session","message_id":"synthetic-message","timestamp_ms":1780000000000,"model":"claude-sonnet-4","mode":"work","agent_type":"solo_work_lite","token_usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":160,"cache_creation_input_tokens":10,"cache_read_input_tokens":10,"reasoning_tokens":5,"prompt_tokens_total":1000,"completion_tokens_total":400,"last_turn_total_tokens":160,"max_tokens":200000}}`})
	if len(warnings) != 0 || len(records) != 1 {
		t.Fatalf("records=%d warnings=%v", len(records), warnings)
	}
	rec := records[0]
	if rec.Agent != "trae-work-cn" || rec.SourceProduct != "trae-work-cn" || rec.Provider != "unknown" {
		t.Fatalf("unexpected source fields: %#v", rec)
	}
	if rec.NativeSessionID != "synthetic-session" || rec.SessionID != "synthetic-session" || rec.NativeEventID != "synthetic-message" || rec.MessageID != "synthetic-message" || rec.IdentityKind != "message" || rec.IdentityScope != "session" {
		t.Fatalf("unexpected identity fields: %#v", rec)
	}
	if rec.TimestampMs != 1780000000000 || rec.Model != "claude-sonnet-4" || rec.ModelNormalized != "claude-sonnet-4" || rec.ModelResolution != model.ModelResolutionDirectEvent || rec.ModelIsFallback {
		t.Fatalf("unexpected timestamp/model fields: %#v", rec)
	}
	if rec.InputTokens != 100 || rec.OutputTokens != 40 || rec.TotalTokens != 160 {
		t.Fatalf("unexpected canonical token fields: %#v", rec)
	}
	if rec.CacheCreationTokens != 0 || rec.CacheReadTokens != 0 || rec.ReasoningTokens != 0 {
		t.Fatalf("cache/reasoning details must not be double-counted: %#v", rec)
	}
	if rec.RawInputTokens == nil || *rec.RawInputTokens != 100 || rec.SourceTotalTokens == nil || *rec.SourceTotalTokens != 160 {
		t.Fatalf("unexpected source token diagnostics: raw=%v source=%v", rec.RawInputTokens, rec.SourceTotalTokens)
	}
	if rec.ObservabilityLevel != "partial" || rec.TokenAccountingMethod != model.AccTraeWorkCNMessageUsage || rec.AccountingProfile != traeWorkCNAccountingProfile || rec.ParserVersion != "trae-work-cn-v1" || rec.Granularity != "message" {
		t.Fatalf("unexpected accounting metadata: %#v", rec)
	}

	assertTraeWorkCNEnvelopeIsPrivate(t, rec.FingerprintJSON)
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(rec.FingerprintJSON), &envelope); err != nil {
		t.Fatalf("unmarshal sanitized envelope: %v", err)
	}
	usage, ok := envelope["token_usage"].(map[string]interface{})
	if !ok || usage["total_tokens"] != float64(160) || usage["cache_read_input_tokens"] != float64(10) || usage["reasoning_tokens"] != float64(5) || usage["prompt_tokens_total"] != float64(1000) {
		t.Fatalf("unexpected sanitized token envelope: %#v", envelope)
	}
	if rec.RawSHA256 == "" || rec.SourceFile != traeWorkCNRuntimeSource || rec.LineNumber != 1 {
		t.Fatalf("missing local diagnostics: %#v", rec)
	}
}

func TestTraeWorkCNAdapterFailsClosedOnInvalidOrUnsanitizedLines(t *testing.T) {
	valid := `{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"valid","timestamp_ms":1780000000000,"mode":"code","agent_type":"solo_code_lite","token_usage":{"total_tokens":1}}`
	invalid := []string{
		`{not-json}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"content","timestamp_ms":1780000000000,"content":"private","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"user","timestamp_ms":1780000000000,"user_info":{"name":"private"},"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"nested","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1,"query":"private"}}`,
		`{"schema":"other","session_id":"session","message_id":"schema","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","message_id":"session","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","timestamp_ms":1780000000000,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"timestamp","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"timestamp-float","timestamp_ms":1780000000000.5,"token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"usage","timestamp_ms":1780000000000}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"total","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"negative","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":-1,"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"float","timestamp_ms":1780000000000,"token_usage":{"completion_tokens":0.5,"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"zero","timestamp_ms":1780000000000,"token_usage":{"total_tokens":0}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"overflow","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":3}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"integer-overflow","timestamp_ms":1780000000000,"token_usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775807}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"mode","timestamp_ms":1780000000000,"mode":"chat","token_usage":{"total_tokens":1}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"agent","timestamp_ms":1780000000000,"agent_type":"assistant","token_usage":{"total_tokens":1}}`,
	}
	lines := append(invalid, valid)
	records, warnings := parseSyntheticTraeWorkCNUsageRecords(lines)
	if len(records) != 1 || records[0].NativeEventID != "valid" {
		t.Fatalf("invalid lines must be skipped and valid data retained: %#v", records)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one aggregated warning, got %#v", warnings)
	}
	for _, want := range []string{
		"skipped 18 invalid TRAE Work CN usage record(s)",
		"invalid_agent_type=1",
		"invalid_json=1",
		"invalid_mode=1",
		"invalid_schema=3",
		"invalid_timestamp=1",
		"invalid_token_totals=3",
		"invalid_token_value=2",
		"missing_required_fields=6",
	} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("warning missing %q: %s", want, warnings[0])
		}
	}
	for _, private := range []string{"private", "content\"", "user_info", "query"} {
		if strings.Contains(warnings[0], private) {
			t.Fatalf("warning leaked rejected source field %q: %s", private, warnings[0])
		}
	}
}

func TestTraeWorkCNMessageIdentityIsStableAcrossFilesAndUsageCorrections(t *testing.T) {
	lines := []string{
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"same-session","message_id":"same-message","timestamp_ms":1780000000000,"model":"auto","token_usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":20}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"same-session","message_id":"same-message","timestamp_ms":1780000000000,"model":"claude-sonnet-4","token_usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":180}}`,
	}
	var eventIDs []string
	for index, line := range lines {
		records, warnings := parseSyntheticTraeWorkCNUsageRecords([]string{line})
		if len(warnings) != 0 || len(records) != 1 {
			t.Fatalf("parse usage record %d: records=%d warnings=%v", index, len(records), warnings)
		}
		_, eventID, strategy, _, err := fingerprint.ComputeIdentity(records[0])
		if err != nil {
			t.Fatalf("compute identity: %v", err)
		}
		if strategy != fingerprint.StrategyNativeMessage {
			t.Fatalf("unexpected strategy %q", strategy)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if eventIDs[0] != eventIDs[1] {
		t.Fatalf("model/token/path changes altered native message identity: %q != %q", eventIDs[0], eventIDs[1])
	}
}

func TestTraeWorkCNAdapterUsesUnknownFallbackForMissingOrAutoModel(t *testing.T) {
	records, warnings := parseSyntheticTraeWorkCNUsageRecords([]string{
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"missing","timestamp_ms":1780000000000,"token_usage":{"total_tokens":5}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"auto","timestamp_ms":1780000000001,"model":"auto","token_usage":{"prompt_tokens":2,"total_tokens":5}}`,
		`{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"invalid","timestamp_ms":1780000000002,"model":"../../private-path","token_usage":{"total_tokens":5}}`,
	})
	if len(warnings) != 0 || len(records) != 3 {
		t.Fatalf("records=%d warnings=%v", len(records), warnings)
	}
	if records[0].Model != "unknown" || records[0].ModelNormalized != "unknown" || records[0].ModelResolution != model.ModelResolutionUnknown || !records[0].ModelIsFallback || records[0].RawInputTokens != nil {
		t.Fatalf("unexpected missing-model fallback: %#v", records[0])
	}
	if records[1].Model != "auto" || records[1].ModelNormalized != "unknown" || records[1].ModelResolution != model.ModelResolutionUnknown || !records[1].ModelIsFallback || records[1].RawInputTokens == nil || *records[1].RawInputTokens != 2 {
		t.Fatalf("unexpected auto-model fallback: %#v", records[1])
	}
	if records[2].Model != "unknown" || records[2].ModelNormalized != "unknown" || records[2].ModelResolution != model.ModelResolutionUnknown || !records[2].ModelIsFallback || strings.Contains(records[2].FingerprintJSON, "private-path") {
		t.Fatalf("unexpected invalid-model fallback: %#v", records[2])
	}
}

func TestTraeWorkCNDiscoverDoesNotDependOnIntermediateFiles(t *testing.T) {
	adapter := NewTraeWorkCNAdapter()
	files, err := adapter.Discover([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("direct runtime adapter unexpectedly discovered files: %#v", files)
	}
	if _, err := adapter.ParseFile(filepath.Join(t.TempDir(), "usage.jsonl")); err == nil {
		t.Fatal("direct runtime adapter unexpectedly accepted file input")
	}
}

func TestTraeWorkCNCollectsStrictRuntimeProjection(t *testing.T) {
	runtime := &fakeTraeWorkCNRuntime{
		payloads: []string{`{
			"schema":"agentledger.trae-work-cn.runtime.v1",
			"sessions_scanned":2,
			"messages_scanned":4,
			"assistant_messages":2,
			"duplicate_messages":0,
			"skipped_missing_identity":0,
			"skipped_invalid_timestamp":0,
			"skipped_missing_usage":1,
			"skipped_invalid_usage":0,
			"records":[{
				"schema":"agentledger.trae-work-cn.usage.v1",
				"session_id":"runtime-session",
				"message_id":"runtime-message",
				"timestamp_ms":1780000000000,
				"model":"gpt-test",
				"mode":"work",
				"agent_type":"solo_work_lite",
				"token_usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140}
			}]
		}`},
		warnings: []string{"synthetic cleanup warning"},
		probe:    DirectSourceProbe{Supported: true, RunningSources: 1},
	}
	adapter := newTraeWorkCNAdapterWithRuntime(runtime)
	records, warnings, err := adapter.Collect(nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(records) != 1 || records[0].TotalTokens != 140 || records[0].InputTokens != 100 || records[0].OutputTokens != 40 {
		t.Fatalf("unexpected runtime records: %#v", records)
	}
	if records[0].SourceFile != traeWorkCNRuntimeSource || records[0].NativeSessionID != "runtime-session" || records[0].NativeEventID != "runtime-message" {
		t.Fatalf("unexpected runtime record identity/diagnostics: %#v", records[0])
	}
	if records[0].ModelNormalized != "gpt-test" || records[0].ModelResolution != model.ModelResolutionDirectEvent || records[0].ModelIsFallback {
		t.Fatalf("unexpected runtime model attribution: %#v", records[0])
	}
	if len(warnings) != 1 || warnings[0] != "synthetic cleanup warning" {
		t.Fatalf("unexpected runtime warnings: %#v", warnings)
	}

	diagnostics := adapter.ImportDiagnostics()
	if len(diagnostics) != 5 || diagnostics[3].Code != "trae_work_cn_unmetered_assistant_messages" || diagnostics[3].Count != 1 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
	usage := diagnostics[4]
	if usage.Code != "trae_work_cn_direct_usage" || usage.Events != 1 || usage.Tokens != 140 {
		t.Fatalf("unexpected usage diagnostic: %#v", usage)
	}
	probe, err := adapter.Probe(nil)
	if err != nil || !probe.Supported || probe.RunningSources != 1 {
		t.Fatalf("unexpected probe: %#v err=%v", probe, err)
	}
}

func TestTraeWorkCNRuntimeProjectionRejectsUnknownOrPrivateFields(t *testing.T) {
	payloads := []string{
		`{"schema":"agentledger.trae-work-cn.runtime.v1","sessions_scanned":1,"messages_scanned":1,"assistant_messages":1,"duplicate_messages":0,"skipped_missing_identity":0,"skipped_invalid_timestamp":0,"skipped_missing_usage":0,"skipped_invalid_usage":0,"content":"private","records":[]}`,
		`{"schema":"agentledger.trae-work-cn.runtime.v1","sessions_scanned":1,"messages_scanned":1,"assistant_messages":1,"duplicate_messages":0,"skipped_missing_identity":0,"skipped_invalid_timestamp":0,"skipped_missing_usage":0,"skipped_invalid_usage":0,"records":[{"schema":"agentledger.trae-work-cn.usage.v1","session_id":"session","message_id":"message","timestamp_ms":1780000000000,"model":"unknown","mode":"work","agent_type":"solo_work_lite","content":"private","token_usage":{"total_tokens":1}}]}`,
	}
	for _, payload := range payloads {
		adapter := newTraeWorkCNAdapterWithRuntime(&fakeTraeWorkCNRuntime{payloads: []string{payload}})
		records, _, err := adapter.Collect(nil)
		if err == nil || len(records) != 0 || strings.Contains(err.Error(), "private") {
			t.Fatalf("private/unknown runtime projection was not rejected safely: records=%#v err=%v", records, err)
		}
	}
}

func TestTraeWorkCNRuntimeCollectorRequiresSupportedRunningProcess(t *testing.T) {
	unsupported := &traeWorkCNRuntimeCollector{platformSupported: func() bool { return false }}
	if _, _, err := unsupported.Collect(context.Background(), nil); err == nil {
		t.Fatal("unsupported collector unexpectedly succeeded")
	}

	missing := &traeWorkCNRuntimeCollector{
		platformSupported: func() bool { return true },
		findProcesses:     func([]string) ([]int, error) { return nil, nil },
	}
	if _, _, err := missing.Collect(context.Background(), nil); err == nil {
		t.Fatal("collector unexpectedly succeeded without a running process")
	}

	partial := &traeWorkCNRuntimeCollector{
		platformSupported: func() bool { return true },
		findProcesses:     func([]string) ([]int, error) { return []int{2, 1}, nil },
		collectProcess: func(_ context.Context, pid int) (string, []string, error) {
			if pid == 1 {
				return "projection", nil, nil
			}
			return "", nil, errors.New("synthetic failure")
		},
	}
	payloads, warnings, err := partial.Collect(context.Background(), nil)
	if err != nil || len(payloads) != 1 || payloads[0] != "projection" || len(warnings) != 1 {
		t.Fatalf("unexpected partial collection: payloads=%#v warnings=%#v err=%v", payloads, warnings, err)
	}
}

func TestMatchesTraeWorkCNExecutableHonorsOptionalBundleAllowlist(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "TRAE SOLO CN.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "Electron")
	if !matchesTraeWorkCNExecutable(executable, nil) {
		t.Fatal("automatic discovery rejected the TRAE Work CN executable")
	}
	if !matchesTraeWorkCNExecutable(executable, []string{bundle}) {
		t.Fatal("bundle allowlist rejected the matching executable")
	}
	if matchesTraeWorkCNExecutable(executable, []string{filepath.Join(root, "Other.app")}) {
		t.Fatal("bundle allowlist accepted a different application")
	}
}

func TestTraeWorkCNRuntimeExpressionProjectsUsageOnly(t *testing.T) {
	for _, required := range []string{
		`service: "lite"`,
		`"list_chat_sessions"`,
		`"get_messages"`,
		`message.token_usage`,
		`model: safeModel(modelMeta?.config_name)`,
		`return JSON.stringify(result)`,
	} {
		if !strings.Contains(traeWorkCNRuntimeExpression, required) {
			t.Fatalf("runtime expression missing required privacy/usage contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"message.content",
		"message.query",
		"session.title",
		"user_info",
		"JSON.stringify(message)",
		"JSON.stringify(payload)",
	} {
		if strings.Contains(traeWorkCNRuntimeExpression, forbidden) {
			t.Fatalf("runtime expression crosses privacy boundary via %q", forbidden)
		}
	}
}

func TestWaitForLoopbackPortClosed(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	openCtx, openCancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer openCancel()
	if waitForLoopbackPortClosed(openCtx, port) {
		t.Fatal("open loopback port was reported closed")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	closedCtx, closedCancel := context.WithTimeout(context.Background(), time.Second)
	defer closedCancel()
	if !waitForLoopbackPortClosed(closedCtx, port) {
		t.Fatal("closed loopback port was reported open")
	}
}

func TestTraeWorkCNDebugSessionRecoversAndClosesInspectorOpenedBeforeCDPAttach(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	const syntheticPID = 4242
	serverErrors := make(chan error, 1)
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			reader := bufio.NewReader(connection)
			request, readErr := http.ReadRequest(reader)
			if readErr != nil {
				_ = connection.Close()
				continue
			}
			switch request.URL.Path {
			case "/json/list":
				body := fmt.Sprintf(`[{"type":"node","webSocketDebuggerUrl":"ws://127.0.0.1:%d/synthetic-node-target"}]`, port)
				_, writeErr := fmt.Fprintf(connection, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				_ = connection.Close()
				if writeErr != nil {
					serverErrors <- writeErr
					return
				}
			case "/synthetic-node-target":
				if err := writeTestWebSocketHandshake(connection, request.Header.Get("Sec-WebSocket-Key")); err != nil {
					serverErrors <- err
					_ = connection.Close()
					return
				}
				_, payload, err := readMaskedClientFrame(reader)
				if err != nil {
					serverErrors <- err
					_ = connection.Close()
					return
				}
				var cdpRequest struct {
					ID int64 `json:"id"`
				}
				if err := json.Unmarshal(payload, &cdpRequest); err != nil {
					serverErrors <- err
					_ = connection.Close()
					return
				}
				response := []byte(fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"number","value":%d}}}`, cdpRequest.ID, syntheticPID))
				if err := writeServerFrame(connection, true, 0x1, response); err != nil {
					serverErrors <- err
					_ = connection.Close()
					return
				}
				if _, _, err := readMaskedClientFrame(reader); err != nil {
					serverErrors <- err
					_ = connection.Close()
					return
				}
				_ = listener.Close()
				_ = connection.Close()
				serverErrors <- nil
				return
			default:
				_ = connection.Close()
			}
		}
	}()

	session := &traeWorkCNDebugSession{
		pid:            syntheticPID,
		mainPort:       port,
		mainOpenedByUs: true,
	}
	if warnings := session.Close(); len(warnings) != 0 {
		t.Fatalf("unexpected cleanup warnings: %#v", warnings)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
	if loopbackPortOpen(port) {
		t.Fatal("recovered inspector port remained open")
	}
}

type fakeTraeWorkCNRuntime struct {
	payloads []string
	warnings []string
	err      error
	probe    DirectSourceProbe
}

func (f *fakeTraeWorkCNRuntime) Collect(context.Context, []string) ([]string, []string, error) {
	return f.payloads, f.warnings, f.err
}

func (f *fakeTraeWorkCNRuntime) Probe([]string) (DirectSourceProbe, error) {
	return f.probe, f.err
}

func parseSyntheticTraeWorkCNUsageRecords(lines []string) ([]*fingerprint.ParsedRecord, []string) {
	records := make([]*fingerprint.ParsedRecord, 0, len(lines))
	diagnostics := newTraeWorkCNParseDiagnostics()
	for index, line := range lines {
		if !json.Valid([]byte(line)) {
			diagnostics.add(index+1, "invalid_json")
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		var snapshot traeWorkCNSnapshot
		if err := decoder.Decode(&snapshot); err != nil {
			diagnostics.add(index+1, "invalid_schema")
			continue
		}
		record, reason := traeWorkCNRecordFromSnapshot(&snapshot, traeWorkCNRuntimeSource, index+1, []byte(line))
		if reason != "" {
			diagnostics.add(index+1, reason)
			continue
		}
		records = append(records, record)
	}
	return records, diagnostics.warnings()
}

func assertTraeWorkCNEnvelopeIsPrivate(t *testing.T, raw string) {
	t.Helper()
	for _, forbidden := range []string{
		"synthetic-session", "synthetic-message", "session_id", "message_id",
		"content", "query", "user_info", "credential", "https://", "url",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("sanitized envelope leaked %q: %s", forbidden, raw)
		}
	}
}
