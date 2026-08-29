package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const (
	cursorAgentExecDir       = "anysphere.cursor-agent-exec"
	cursorLogTimestampLayout = "2006-01-02 15:04:05.000"
	cursorAccountingProfile  = "cursor_agent_exec_v1"
)

var cursorUsageMessages = []string{
	"[info] Setting token details for client token ring",
	"[info] Setting token details for model-stream-only client token ring",
}

// CursorAdapter imports explicit per-request usage emitted by Cursor Agent Exec.
// The log does not expose a native request or conversation ID, so events use a
// global content fallback and Session remains an explicit local-date log bucket.
type CursorAdapter struct {
	semanticDuplicates int64
}

func NewCursorAdapter() *CursorAdapter { return &CursorAdapter{} }

func (a *CursorAdapter) Name() string { return "cursor" }

func (a *CursorAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{
			"~/Library/Application Support/Cursor/logs",
			"~/Library/Application Support/Cursor Private Inference/logs",
			"~/.config/Cursor/logs",
			"~/AppData/Roaming/Cursor/logs",
		}
	}
	files, err := DiscoverFiles(paths, []string{".log"})
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(files))
	for _, path := range files {
		if cursorAgentExecLog(path) {
			filtered = append(filtered, filepath.Clean(path))
		}
	}
	filtered = uniqueExistingFiles(filtered)
	sort.Strings(filtered)
	return filtered, nil
}

func cursorAgentExecLog(path string) bool {
	extensionDir := filepath.Dir(path)
	if !strings.EqualFold(filepath.Base(extensionDir), cursorAgentExecDir) ||
		!strings.EqualFold(filepath.Base(filepath.Dir(extensionDir)), "exthost") {
		return false
	}
	name := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(name, "cursor agent exec.") && strings.HasSuffix(name, ".log")
}

func (a *CursorAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	records, _, err := a.ParseFileWithWarnings(path)
	return records, err
}

func (a *CursorAdapter) ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open Cursor source %s: %w", path, err)
	}
	defer f.Close()

	records := make([]*fingerprint.ParsedRecord, 0)
	diagnostics := newCursorParseDiagnostics()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		record, reason, matched := cursorRecordFromLine(scanner.Bytes(), path, lineNumber)
		if !matched {
			continue
		}
		if record != nil {
			records = append(records, record)
		} else if reason != "" {
			diagnostics.add(lineNumber, reason)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, diagnostics.warnings(), fmt.Errorf("scan Cursor source %s: %w", path, err)
	}
	return records, diagnostics.warnings(), nil
}

type cursorUsageEnvelope struct {
	Action           *string `json:"action"`
	ModelName        *string `json:"modelName"`
	UsedTokens       *int64  `json:"usedTokens"`
	InputTokens      *int64  `json:"inputTokens"`
	OutputTokens     *int64  `json:"outputTokens"`
	CacheReadTokens  *int64  `json:"cacheReadTokens"`
	CacheWriteTokens *int64  `json:"cacheWriteTokens"`
}

func cursorRecordFromLine(sourceLine []byte, path string, lineNumber int) (*fingerprint.ParsedRecord, string, bool) {
	line := strings.TrimSpace(string(sourceLine))
	messageIndex, messageLength := cursorUsageMessageLocation(line)
	if messageIndex < 0 {
		return nil, "", false
	}

	timestampText := strings.TrimSpace(line[:messageIndex])
	timestamp, err := time.ParseInLocation(cursorLogTimestampLayout, timestampText, time.Local)
	if err != nil {
		return nil, "invalid_timestamp", true
	}
	payload := strings.TrimSpace(line[messageIndex+messageLength:])
	var usage cursorUsageEnvelope
	decoder := json.NewDecoder(strings.NewReader(payload))
	if err := decoder.Decode(&usage); err != nil {
		return nil, "invalid_json", true
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, "invalid_json", true
	}

	if usage.Action == nil || usage.ModelName == nil || usage.UsedTokens == nil ||
		usage.InputTokens == nil || usage.OutputTokens == nil || usage.CacheReadTokens == nil || usage.CacheWriteTokens == nil {
		return nil, "missing_required_fields", true
	}
	action := strings.TrimSpace(*usage.Action)
	modelRaw := strings.TrimSpace(*usage.ModelName)
	used := *usage.UsedTokens
	rawInput := *usage.InputTokens
	output := *usage.OutputTokens
	cacheRead := *usage.CacheReadTokens
	cacheWrite := *usage.CacheWriteTokens
	if action == "" || modelRaw == "" {
		return nil, "missing_required_fields", true
	}
	if used < 0 || rawInput < 0 || output < 0 || cacheRead < 0 || cacheWrite < 0 {
		return nil, "invalid_token_totals", true
	}
	if rawInput > math.MaxInt64-output || used != rawInput+output {
		return nil, "invalid_token_totals", true
	}
	if cacheWrite > rawInput || cacheRead > rawInput-cacheWrite {
		return nil, "invalid_token_details", true
	}
	if used == 0 {
		return nil, "", true
	}

	sessionPathID := "cursor-agent-exec/" + timestamp.Format("2006-01-02")
	canonicalInput := rawInput - cacheRead - cacheWrite
	modelNormalized := model.CanonicalModelID(modelRaw)
	if modelNormalized == "" {
		return nil, "missing_required_fields", true
	}
	sourceTotal := used
	rawInputCopy := rawInput

	return &fingerprint.ParsedRecord{
		Agent:                 "cursor",
		Provider:              "unknown",
		Model:                 modelRaw,
		ModelNormalized:       modelNormalized,
		ModelResolution:       model.ModelResolutionDirectEvent,
		SessionPathID:         sessionPathID,
		IdentityScope:         "global",
		IdentitySubkey:        action,
		ParserVersion:         "cursor-agent-exec-v1",
		Granularity:           "request",
		TimestampMs:           timestamp.UnixMilli(),
		SessionID:             sessionPathID,
		InputTokens:           canonicalInput,
		OutputTokens:          output,
		CacheCreationTokens:   cacheWrite,
		CacheReadTokens:       cacheRead,
		TotalTokens:           used,
		SourceTotalTokens:     &sourceTotal,
		RawInputTokens:        &rawInputCopy,
		SourceProduct:         "cursor-agent-exec",
		ObservabilityLevel:    "partial",
		TokenAccountingMethod: model.AccCursorAgentExec,
		AccountingProfile:     cursorAccountingProfile,
		FingerprintJSON:       payload,
		SourceFile:            path,
		LineNumber:            lineNumber,
		RawSHA256:             sha256Hex(sourceLine),
	}, "", true
}

func cursorUsageMessageLocation(line string) (int, int) {
	for _, message := range cursorUsageMessages {
		if index := strings.Index(line, message); index >= 0 {
			return index, len(message)
		}
	}
	return -1, 0
}

type cursorSemanticIdentity struct {
	TimestampMs       int64
	Action            string
	Model             string
	RawInputTokens    int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	SourceTotalTokens int64
}

func cursorSemanticKey(record *fingerprint.ParsedRecord) cursorSemanticIdentity {
	rawInput := int64(0)
	if record.RawInputTokens != nil {
		rawInput = *record.RawInputTokens
	}
	sourceTotal := record.TotalTokens
	if record.SourceTotalTokens != nil {
		sourceTotal = *record.SourceTotalTokens
	}
	return cursorSemanticIdentity{
		TimestampMs:       record.TimestampMs,
		Action:            strings.TrimSpace(record.IdentitySubkey),
		Model:             strings.ToLower(model.CanonicalModelID(record.Model)),
		RawInputTokens:    rawInput,
		OutputTokens:      record.OutputTokens,
		CacheReadTokens:   record.CacheReadTokens,
		CacheWriteTokens:  record.CacheCreationTokens,
		SourceTotalTokens: sourceTotal,
	}
}

// PostProcessRecords removes identical usage rows copied across Cursor log
// segments. It cannot distinguish two real calls whose source evidence is
// identical in the same millisecond because Cursor emits no request ID.
func (a *CursorAdapter) PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord {
	a.semanticDuplicates = 0
	canonical := make(map[cursorSemanticIdentity]*fingerprint.ParsedRecord, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		key := cursorSemanticKey(record)
		existing, ok := canonical[key]
		if !ok {
			canonical[key] = record
			continue
		}
		a.semanticDuplicates++
		if cursorRecordLocationLess(record, existing) {
			canonical[key] = record
		}
	}
	result := make([]*fingerprint.ParsedRecord, 0, len(canonical))
	for _, record := range canonical {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TimestampMs != result[j].TimestampMs {
			return result[i].TimestampMs < result[j].TimestampMs
		}
		return cursorRecordLocationLess(result[i], result[j])
	})
	return result
}

func cursorRecordLocationLess(left, right *fingerprint.ParsedRecord) bool {
	if left.SessionPathID != right.SessionPathID {
		return left.SessionPathID < right.SessionPathID
	}
	if filepath.Clean(left.SourceFile) != filepath.Clean(right.SourceFile) {
		return filepath.Clean(left.SourceFile) < filepath.Clean(right.SourceFile)
	}
	return left.LineNumber < right.LineNumber
}

func (a *CursorAdapter) ImportDiagnostics() []ImportDiagnostic {
	return []ImportDiagnostic{{
		Code:   "cursor_semantic_duplicates",
		Unit:   ImportDiagnosticUnitEvents,
		Events: a.semanticDuplicates,
	}}
}

type cursorParseDiagnostics struct {
	total   int
	reasons map[string]int
	samples []string
}

func newCursorParseDiagnostics() *cursorParseDiagnostics {
	return &cursorParseDiagnostics{reasons: make(map[string]int)}
}

func (d *cursorParseDiagnostics) add(lineNumber int, reason string) {
	d.total++
	d.reasons[reason]++
	if len(d.samples) < 5 {
		d.samples = append(d.samples, fmt.Sprintf("line %d %s", lineNumber, reason))
	}
}

func (d *cursorParseDiagnostics) warnings() []string {
	if d.total == 0 {
		return nil
	}
	reasons := make([]string, 0, len(d.reasons))
	for reason := range d.reasons {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	counts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		counts = append(counts, fmt.Sprintf("%s=%d", reason, d.reasons[reason]))
	}
	warning := fmt.Sprintf("skipped %d invalid Cursor usage line(s) (%s); samples: %s", d.total, strings.Join(counts, ", "), strings.Join(d.samples, ", "))
	if d.total > len(d.samples) {
		warning += fmt.Sprintf(", ... %d more", d.total-len(d.samples))
	}
	return []string{warning}
}
