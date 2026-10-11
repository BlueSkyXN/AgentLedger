package adapters

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/klauspost/compress/zstd"
)

func zstdCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("new zstd encoder: %v", err)
	}
	if _, err := encoder.Write(data); err != nil {
		t.Fatalf("zstd encode: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// 同一 rollout 以未压缩与压缩两种形态解析时，必须产出相同的记录与身份：
// 这是 Codex local_thread_store_compression 冷压缩前后增量导入幂等的前提。
func TestCodexZstTwinYieldsIdenticalRecordsAndIdentity(t *testing.T) {
	lines := []string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","model":"gpt-5","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","model":"gpt-5","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23}}}}`,
	}
	data := []byte(strings.Join(lines, "\n") + "\n")

	plainPath := filepath.Join(t.TempDir(), "sessions", "2026", "01", "01", "rollout-session.jsonl")
	zstPath := filepath.Join(t.TempDir(), "sessions", "2026", "01", "01", "rollout-session.jsonl.zst")
	for _, path := range []string{plainPath, zstPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		content := data
		if isCodexRolloutZstPath(path) {
			content = zstdCompress(t, data)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	var eventIDs []string
	for index, path := range []string{plainPath, zstPath} {
		records, err := NewCodexAdapter().ParseFile(path)
		if err != nil || len(records) != 2 {
			t.Fatalf("parse %d: records=%d err=%v", index, len(records), err)
		}
		if records[0].SessionPathID != "2026/01/01/rollout-session" {
			t.Fatalf("parse %d: unexpected session path id=%q", index, records[0].SessionPathID)
		}
		if records[0].SourceFile != path {
			t.Fatalf("parse %d: source file diagnostic not preserved: %q", index, records[0].SourceFile)
		}
		_, eventID, strategy, _, err := fingerprint.ComputeIdentity(records[0])
		if err != nil || strategy != fingerprint.StrategySessionRecord {
			t.Fatalf("parse %d: compute identity strategy=%s err=%v", index, strategy, err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if eventIDs[0] != eventIDs[1] {
		t.Fatalf("compression changed session-record identity: %q != %q", eventIDs[0], eventIDs[1])
	}
}

// zstd 追加压缩会产生多帧流；解码器必须跨帧继续读，不能只解第一帧。
func TestCodexZstMultiFrameStreamParses(t *testing.T) {
	first := `{"type":"event_msg","timestamp":"2026-01-01T00:00:00Z","model":"gpt-5","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}` + "\n"
	second := `{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","model":"gpt-5","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23}}}}` + "\n"
	data := append(zstdCompress(t, []byte(first)), zstdCompress(t, []byte(second))...)

	path := filepath.Join(t.TempDir(), "rollout-multi.jsonl.zst")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	records, err := NewCodexAdapter().ParseFile(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
}

func TestCodexDiscoverHandlesCompressedRollouts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	plain := filepath.Join(dir, "rollout-a.jsonl")
	twin := filepath.Join(dir, "rollout-a.jsonl.zst")
	other := filepath.Join(dir, "rollout-b.jsonl.zst")
	stray := filepath.Join(dir, "notes.log.zst")
	if err := os.WriteFile(plain, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	if err := os.WriteFile(twin, zstdCompress(t, []byte("{}\n")), 0o644); err != nil {
		t.Fatalf("write twin: %v", err)
	}
	if err := os.WriteFile(other, zstdCompress(t, []byte("{}\n")), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}
	if err := os.WriteFile(stray, zstdCompress(t, []byte("noise\n")), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	files, err := NewCodexAdapter().Discover([]string{root})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var kept []string
	for _, file := range files {
		switch file {
		case plain, twin, other, stray:
			kept = append(kept, file)
		}
	}
	if len(kept) != 2 {
		t.Fatalf("expected plain rollout-a plus compressed rollout-b only, got %v", kept)
	}
	for _, file := range kept {
		if file == twin {
			t.Fatalf("compressed twin should lose dedupe against the live .jsonl: %v", kept)
		}
		if file == stray {
			t.Fatalf("non-rollout .zst must not be discovered: %v", kept)
		}
	}
}

func TestCodexExtractIdentityFromZstPath(t *testing.T) {
	zst := filepath.Join("home", ".codex", "sessions", "2026", "05", "27", "rollout-abc.jsonl.zst")
	if got := extractCodexSession(zst); got != "rollout-abc" {
		t.Fatalf("extractCodexSession(.zst)=%q", got)
	}
	if got := extractCodexSessionPathID(zst); got != "2026/05/27/rollout-abc" {
		t.Fatalf("extractCodexSessionPathID(.zst)=%q", got)
	}
	plain := filepath.Join("home", ".codex", "sessions", "2026", "05", "27", "rollout-abc.jsonl")
	if extractCodexSession(zst) != extractCodexSession(plain) {
		t.Fatalf("session id differs across compressed twin")
	}
	if extractCodexSessionPathID(zst) != extractCodexSessionPathID(plain) {
		t.Fatalf("session path id differs across compressed twin")
	}
}

// 损坏的压缩流必须显式报错进入既有的 parse-error 语义，不允许静默当作空文件。
func TestCodexTruncatedZstSurfacesError(t *testing.T) {
	data := zstdCompress(t, bytes.Repeat([]byte(`{"type":"event_msg"}`+"\n"), 8))
	path := filepath.Join(t.TempDir(), "rollout-broken.jsonl.zst")
	if err := os.WriteFile(path, data[:len(data)/2], 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := NewCodexAdapter().ParseFile(path); err == nil {
		t.Fatalf("expected parse error for truncated zstd stream")
	}
}
