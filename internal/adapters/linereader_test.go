package adapters

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

type scannedLine struct {
	text      string
	oversized bool
}

func readAllLines(t *testing.T, reader *jsonlLineReader) []scannedLine {
	t.Helper()
	var lines []scannedLine
	for reader.Scan() {
		lines = append(lines, scannedLine{text: string(reader.Bytes()), oversized: reader.Oversized()})
	}
	if err := reader.Err(); err != nil {
		t.Fatalf("read error: %v", err)
	}
	return lines
}

func TestJSONLLineReaderMatchesScannerLineSplitting(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"a\n",
		"a\nb",
		"a\nb\n",
		"\n\n",
		"a\r\nb\r\n",
		"a\n\nb\n",
		"trailing-cr\r",
	}
	for _, input := range inputs {
		scanner := bufio.NewScanner(strings.NewReader(input))
		var want []string
		for scanner.Scan() {
			want = append(want, scanner.Text())
		}
		got := readAllLines(t, newJSONLLineReader(strings.NewReader(input)))
		if len(got) != len(want) {
			t.Fatalf("input %q: got %d lines %+v, want %d %q", input, len(got), got, len(want), want)
		}
		for index := range want {
			if got[index].text != want[index] || got[index].oversized {
				t.Fatalf("input %q line %d: got %+v want %q", input, index, got[index], want[index])
			}
		}
	}
}

func TestJSONLLineReaderSkipsOversizedLinesAndKeepsNumbering(t *testing.T) {
	long := strings.Repeat("x", 100)
	exact := strings.Repeat("y", 16)
	input := "first\n" + long + "\n" + exact + "\n" + long + "\nlast"
	// One-byte reads and a minimal buffer force every ReadSlice boundary case,
	// including bufio.ErrBufferFull in the middle of a line.
	reader := newJSONLLineReaderWithLimit(nil, 16)
	reader.reader = bufio.NewReaderSize(iotest.OneByteReader(strings.NewReader(input)), 16)

	got := readAllLines(t, reader)
	want := []scannedLine{
		{text: "first"},
		{text: "", oversized: true},
		{text: exact},
		{text: "", oversized: true},
		{text: "last"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("line %d: got %+v want %+v", index+1, got[index], want[index])
		}
	}
	if reader.SkippedLines() != 2 {
		t.Fatalf("skipped = %d, want 2", reader.SkippedLines())
	}
}

func TestJSONLLineReaderOversizedFinalLineWithoutNewline(t *testing.T) {
	reader := newJSONLLineReaderWithLimit(strings.NewReader("ok\n"+strings.Repeat("z", 40)), 8)
	got := readAllLines(t, reader)
	if len(got) != 2 || got[0].text != "ok" || !got[1].oversized || reader.SkippedLines() != 1 {
		t.Fatalf("got %+v skipped=%d", got, reader.SkippedLines())
	}
}

func withJSONLLineLimit(t *testing.T, limit int) {
	t.Helper()
	previous := jsonlMaxLineBytes
	jsonlMaxLineBytes = limit
	t.Cleanup(func() { jsonlMaxLineBytes = previous })
}

func TestClaudeAdapterSkipsOversizedLineAndKeepsLaterUsage(t *testing.T) {
	withJSONLLineLimit(t, 1024)
	oversized := `{"type":"user","message":{"content":"` + strings.Repeat("x", 4096) + `"}}`
	path := writeClaudeUsageFile(t,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:05Z","requestId":"req-1","sessionId":"s","message":{"id":"msg-1","model":"claude-opus-5","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		oversized,
		`{"type":"assistant","timestamp":"2026-01-02T03:04:07Z","requestId":"req-2","sessionId":"s","message":{"id":"msg-2","model":"claude-opus-5","usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	records, warnings, err := NewClaudeAdapter().ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("an oversized line must not fail the file: %v", err)
	}
	if len(records) != 2 || records[0].LineNumber != 1 || records[1].LineNumber != 3 {
		t.Fatalf("expected usage on lines 1 and 3, got %+v", records)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "skipped 1 oversized line") {
		t.Fatalf("expected one oversized-line warning, got %q", warnings)
	}
}

func TestCodexAdapterSkipsOversizedLineAndKeepsCumulativeDeltas(t *testing.T) {
	withJSONLLineLimit(t, 1024)
	oversized := `{"type":"event_msg","timestamp":"2026-01-01T00:01:30Z","payload":{"type":"item_completed","output":"` + strings.Repeat("y", 4096) + `"}}`
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	data := strings.Join([]string{
		`{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","session_id":"A","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100}}}}`,
		oversized,
		`{"type":"event_msg","timestamp":"2026-01-01T00:02:00Z","session_id":"A","model":"gpt-5","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, warnings, err := NewCodexAdapter().ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("an oversized line must not fail the file: %v", err)
	}
	if len(records) != 2 || records[0].TotalTokens != 100 || records[1].TotalTokens != 50 || records[1].LineNumber != 3 {
		t.Fatalf("unexpected codex records: %+v", records)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one oversized-line warning, got %q", warnings)
	}
}
