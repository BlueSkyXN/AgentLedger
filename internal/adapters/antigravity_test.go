package adapters

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

// antigravityFixtureSchema mirrors the table layout the Antigravity CLI writes
// (reverse-engineered contract, no real session data).
const antigravityFixtureSchema = `
CREATE TABLE trajectory_meta (trajectory_id text, cascade_id text, trajectory_type integer, source integer, PRIMARY KEY (trajectory_id));
CREATE TABLE steps (idx integer, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0,
	has_subtrajectory numeric NOT NULL DEFAULT false, metadata blob, error_details blob, permissions blob,
	task_details blob, render_info blob, step_payload blob, step_format integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));
CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));
CREATE TABLE executor_metadata (idx integer, data blob, PRIMARY KEY (idx));
CREATE TABLE parent_references (idx integer, data blob, PRIMARY KEY (idx));
CREATE TABLE trajectory_metadata_blob (id text DEFAULT "main", data blob, PRIMARY KEY (id));
CREATE TABLE battle_mode_infos (idx integer, data blob, PRIMARY KEY (idx));
`

// Minimal protobuf encoder for synthetic fixture blobs.
type protoBuilder struct {
	buf []byte
}

func appendProtoVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func (b *protoBuilder) varintField(number int, value uint64) {
	b.buf = appendProtoVarint(b.buf, uint64(number)<<3|0)
	b.buf = appendProtoVarint(b.buf, value)
}

func (b *protoBuilder) bytesField(number int, payload []byte) {
	b.buf = appendProtoVarint(b.buf, uint64(number)<<3|2)
	b.buf = appendProtoVarint(b.buf, uint64(len(payload)))
	b.buf = append(b.buf, payload...)
}

func (b *protoBuilder) textField(number int, text string) {
	b.bytesField(number, []byte(text))
}

func (b *protoBuilder) packedField(number int, values ...uint64) {
	packed := &protoBuilder{}
	for _, value := range values {
		packed.buf = appendProtoVarint(packed.buf, value)
	}
	b.bytesField(number, packed.buf)
}

// protoTimestampMessage encodes {seconds, nanos} from milliseconds.
func protoTimestampMessage(ms int64) []byte {
	timestamp := &protoBuilder{}
	timestamp.varintField(1, uint64(ms/1000))
	timestamp.varintField(2, uint64(ms%1000)*1_000_000)
	return timestamp.buf
}

type antigravityFixtureUsage struct {
	modelID             uint64
	hasModelID          bool
	inputTokens         uint64
	totalOutputTokens   uint64
	cacheCreationTokens uint64
	cacheReadTokens     uint64
	reasoningTokens     uint64
	visibleOutputTokens uint64
	provider            uint64
	hasProvider         bool
	messageID           string
	responseID          string
	providerAssignedID  string
}

func (u antigravityFixtureUsage) blob() []byte {
	usage := &protoBuilder{}
	if u.hasModelID {
		usage.varintField(1, u.modelID)
	}
	usage.varintField(2, u.inputTokens)
	usage.varintField(3, u.totalOutputTokens)
	usage.varintField(4, u.cacheCreationTokens)
	usage.varintField(5, u.cacheReadTokens)
	if u.hasProvider {
		usage.varintField(6, u.provider)
	}
	if u.messageID != "" {
		usage.textField(7, u.messageID)
	}
	usage.varintField(9, u.reasoningTokens)
	usage.varintField(10, u.visibleOutputTokens)
	if u.responseID != "" {
		usage.textField(11, u.responseID)
	}
	if u.providerAssignedID != "" {
		usage.textField(12, u.providerAssignedID)
	}
	return usage.buf
}

type antigravityFixtureGen struct {
	idx          int64
	usage        *antigravityFixtureUsage
	retries      []antigravityFixtureUsage
	modelDisplay string
	modelID      uint64
	hasModelID   bool
	timestampMs  int64
	stepIndices  []int64
}

func (g antigravityFixtureGen) blob() []byte {
	chat := &protoBuilder{}
	if g.hasModelID {
		chat.varintField(3, g.modelID)
	}
	if g.usage != nil {
		chat.bytesField(4, g.usage.blob())
	}
	for _, retry := range g.retries {
		retryInfo := &protoBuilder{}
		retryInfo.bytesField(2, retry.blob())
		chat.bytesField(17, retryInfo.buf)
	}
	if g.timestampMs > 0 {
		info := &protoBuilder{}
		info.bytesField(4, protoTimestampMessage(g.timestampMs))
		chat.bytesField(9, info.buf)
	}
	if g.modelDisplay != "" {
		chat.textField(19, g.modelDisplay)
	}
	root := &protoBuilder{}
	root.bytesField(1, chat.buf)
	if len(g.stepIndices) > 0 {
		values := make([]uint64, 0, len(g.stepIndices))
		for _, idx := range g.stepIndices {
			values = append(values, uint64(idx))
		}
		root.packedField(2, values...)
	}
	return root.buf
}

type antigravityFixtureStep struct {
	idx            int64
	usage          *antigravityFixtureUsage
	retries        []antigravityFixtureUsage
	modelInfoName  string
	modelInfoID    uint64
	hasModelInfoID bool
	timestampMs    int64
}

func (s antigravityFixtureStep) blob() []byte {
	root := &protoBuilder{}
	if s.timestampMs > 0 {
		root.bytesField(1, protoTimestampMessage(s.timestampMs))
	}
	if s.usage != nil {
		root.bytesField(9, s.usage.blob())
	}
	for _, retry := range s.retries {
		retryInfo := &protoBuilder{}
		retryInfo.bytesField(2, retry.blob())
		root.bytesField(28, retryInfo.buf)
	}
	if s.modelInfoName != "" || s.hasModelInfoID {
		info := &protoBuilder{}
		if s.hasModelInfoID {
			info.varintField(1, s.modelInfoID)
		}
		if s.modelInfoName != "" {
			info.textField(12, s.modelInfoName)
		}
		root.bytesField(24, info.buf)
	}
	return root.buf
}

// antigravityFixtureDB writes a synthetic conversation database.
func antigravityFixtureDB(t *testing.T, path string, gens []antigravityFixtureGen, steps []antigravityFixtureStep, trajectoryMs int64) {
	t.Helper()
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(antigravityFixtureSchema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	for _, gen := range gens {
		if _, err := conn.Exec("INSERT INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)", gen.idx, gen.blob(), len(gen.blob())); err != nil {
			t.Fatalf("insert gen_metadata idx %d: %v", gen.idx, err)
		}
	}
	for _, step := range steps {
		if _, err := conn.Exec("INSERT INTO steps (idx, metadata) VALUES (?, ?)", step.idx, step.blob()); err != nil {
			t.Fatalf("insert steps idx %d: %v", step.idx, err)
		}
	}
	if trajectoryMs > 0 {
		trajectory := &protoBuilder{}
		trajectory.bytesField(2, protoTimestampMessage(trajectoryMs))
		if _, err := conn.Exec("INSERT INTO trajectory_metadata_blob (id, data) VALUES ('main', ?)", trajectory.buf); err != nil {
			t.Fatalf("insert trajectory metadata: %v", err)
		}
	}
}

func antigravityParseFixture(t *testing.T, path string) []*fingerprint.ParsedRecord {
	t.Helper()
	adapter := NewAntigravityAdapter()
	records, _, err := adapter.ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	return records
}

func TestAntigravityDiscoverRoots(t *testing.T) {
	root := t.TempDir()
	cliConversations := filepath.Join(root, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(cliConversations, 0o755); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}
	sessionA := filepath.Join(cliConversations, "aaaaaaaa-0000-0000-0000-000000000000.db")
	sessionB := filepath.Join(cliConversations, "bbbbbbbb-0000-0000-0000-000000000000.db")
	for _, path := range []string{sessionA, sessionB} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture db: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(cliConversations, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write non-db file: %v", err)
	}
	// A database outside conversations/ must not be discovered through the
	// nested root, but an explicit file path is honored.
	outside := filepath.Join(root, ".gemini", "antigravity-cli", "conversation_summaries.db")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatalf("write summaries db: %v", err)
	}

	adapter := NewAntigravityAdapter()

	// The parent directory resolves to its conversations/ child.
	files, err := adapter.Discover([]string{filepath.Join(root, ".gemini", "antigravity-cli")})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 conversation databases, got %v", files)
	}

	// Missing roots are skipped, explicit files are accepted, and duplicates
	// via different configured paths collapse.
	files, err = adapter.Discover([]string{
		filepath.Join(root, "does-not-exist"),
		cliConversations,
		filepath.Join(root, ".gemini", "antigravity-cli"),
		sessionA,
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected deduplicated 2 databases, got %v", files)
	}
}

func TestAntigravityParsesGenAndStepMirror(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cccccccc-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{
		modelID:             1427,
		hasModelID:          true,
		inputTokens:         100,
		totalOutputTokens:   50,
		visibleOutputTokens: 20,
		reasoningTokens:     30,
		provider:            24,
		hasProvider:         true,
		messageID:           "msg-fixture-1",
	}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{
			idx:          0,
			usage:        &usage,
			modelDisplay: "gemini-4-carbon",
			modelID:      1427,
			hasModelID:   true,
			stepIndices:  []int64{1},
		}},
		[]antigravityFixtureStep{{
			idx:         1,
			usage:       &usage,
			timestampMs: 1_700_000_005_000,
		}},
		1_700_000_000_000,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected the step/gen mirror to merge into 1 record, got %d", len(records))
	}
	record := records[0]
	if record.TimestampMs != 1_700_000_005_000 {
		t.Fatalf("expected the step timestamp to date the mirror, got %d", record.TimestampMs)
	}
	if record.InputTokens != 100 || record.OutputTokens != 20 || record.ReasoningTokens != 30 || record.TotalTokens != 150 {
		t.Fatalf("unexpected token split: %+v", record)
	}
	if record.ModelNormalized != "gemini-4-carbon" {
		t.Fatalf("expected resolved display model, got %q", record.ModelNormalized)
	}
	if record.Provider != "google" {
		t.Fatalf("expected google provider, got %q", record.Provider)
	}
	if record.NativeEventID != "msg-fixture-1" || record.IdentityKind != "message" || record.IdentityScope != "global" {
		t.Fatalf("unexpected identity: id=%q kind=%q scope=%q", record.NativeEventID, record.IdentityKind, record.IdentityScope)
	}
	if record.Agent != "antigravity" || record.SourceProduct != "antigravity" {
		t.Fatalf("unexpected channel fields: %+v", record)
	}
	if record.TokenAccountingMethod != model.AccAntigravityModelUsage || record.AccountingProfile != (model.AntigravityAccountingEvidence{Time: model.AntigravityStepTime, Output: model.AntigravityReportedOutputWithSplit}).Profile() {
		t.Fatalf("unexpected accounting: %q %q", record.TokenAccountingMethod, record.AccountingProfile)
	}
	if record.ParserVersion != model.AntigravityParser || record.Granularity != "request" {
		t.Fatalf("unexpected parser metadata: %+v", record)
	}
}

func TestAntigravityGenOnlyUsesTrajectoryFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dddddddd-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{inputTokens: 7, totalOutputTokens: 3, visibleOutputTokens: 3, messageID: "msg-fixture-2"}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 0, usage: &usage}},
		nil,
		1_700_000_000_000,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].TimestampMs != 1_700_000_000_000 {
		t.Fatalf("expected trajectory fallback timestamp, got %d", records[0].TimestampMs)
	}
}

func TestAntigravityMissingTimestampSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eeeeeeee-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{inputTokens: 7, totalOutputTokens: 3, visibleOutputTokens: 3, messageID: "msg-fixture-3"}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 0, usage: &usage}}, nil, 0)

	adapter := NewAntigravityAdapter()
	records, _, err := adapter.ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected undated record to be skipped, got %d", len(records))
	}
	found := false
	for _, diagnostic := range adapter.ImportDiagnostics() {
		if diagnostic.Code == "antigravity_skipped_missing_timestamp" && diagnostic.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected antigravity_skipped_missing_timestamp diagnostic, got %+v", adapter.ImportDiagnostics())
	}
}

func TestAntigravityRetryMirrorDroppedDistinctKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffffffff-0000-0000-0000-000000000000.db")
	primary := antigravityFixtureUsage{inputTokens: 10, totalOutputTokens: 4, visibleOutputTokens: 4, messageID: "msg-primary"}
	mirror := primary // retry_infos repeats the primary attempt
	distinct := antigravityFixtureUsage{inputTokens: 20, totalOutputTokens: 8, visibleOutputTokens: 8, messageID: "msg-retry"}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 0, usage: &primary, retries: []antigravityFixtureUsage{mirror, distinct}}},
		nil,
		1_700_000_000_000,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 2 {
		t.Fatalf("expected primary + distinct retry, got %d records", len(records))
	}
	byID := map[string]*fingerprint.ParsedRecord{}
	for _, record := range records {
		byID[record.NativeEventID] = record
	}
	if byID["msg-primary"] == nil || byID["msg-retry"] == nil {
		t.Fatalf("expected both request ids, got %v", byID)
	}
	if byID["msg-primary"].InputTokens != 10 || byID["msg-retry"].InputTokens != 20 {
		t.Fatalf("unexpected retry accounting: %+v", byID)
	}
}

func TestAntigravityTokenDerivation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "11111111-0000-0000-0000-000000000000.db")
	// visible=0 forces the visible/reasoning split to be derived from the
	// authoritative total output.
	usage := antigravityFixtureUsage{
		inputTokens:         10,
		totalOutputTokens:   100,
		reasoningTokens:     40,
		visibleOutputTokens: 0,
		messageID:           "msg-split",
	}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 0, usage: &usage}}, nil, 1_700_000_000_000)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	record := records[0]
	if record.OutputTokens != 60 || record.ReasoningTokens != 40 || record.TotalTokens != 110 {
		t.Fatalf("unexpected derived split: output=%d reasoning=%d total=%d", record.OutputTokens, record.ReasoningTokens, record.TotalTokens)
	}
}

func TestAntigravityModelNormalization(t *testing.T) {
	cases := []struct {
		raw      string
		expected string
	}{
		{"gemini 3 pro", "gemini-3-pro"},
		{"Gemini 3.6 Flash (high)", "gemini-3.6-flash-high"},
		{"gemini 3.6 flash thinking", "gemini-3.6-flash-thinking"},
		{"claude 3.7 sonnet thinking", "claude-3-7-sonnet"},
		{"Gemini 4 Carbon", "gemini-4-carbon"},
		{"model_placeholder_m71", "gemini-3.6-flash-high"},
		{"gpt-oss-120b-medium", "gpt-oss-120b-medium"},
	}
	for _, testCase := range cases {
		normalized, err := normalizeAntigravityModel(testCase.raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", testCase.raw, err)
		}
		if normalized != testCase.expected {
			t.Fatalf("normalize %q = %q, want %q", testCase.raw, normalized, testCase.expected)
		}
	}
	if _, err := normalizeAntigravityModel("  "); err == nil {
		t.Fatal("expected blank model name to be rejected")
	}
	if got := antigravityModelNameFromID(246); got != "gemini-2.5-pro" {
		t.Fatalf("model id 246 = %q", got)
	}
	if got := antigravityModelNameFromID(1427); got != "model_placeholder_m427" {
		t.Fatalf("model id 1427 = %q", got)
	}
}

func TestAntigravityFallbackRecordIdentity(t *testing.T) {
	session := "22222222-0000-0000-0000-000000000000"
	path := filepath.Join(t.TempDir(), session+".db")
	usage := antigravityFixtureUsage{inputTokens: 5, totalOutputTokens: 5, visibleOutputTokens: 5}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 1, usage: &usage, stepIndices: []int64{2}}},
		[]antigravityFixtureStep{{idx: 2, usage: &usage, timestampMs: 1_700_000_007_000}},
		0,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected id-less mirror to merge into 1 record, got %d", len(records))
	}
	record := records[0]
	if record.IdentityKind != "record" || record.IdentityScope != "session" || record.IdentitySubkey != "step:2" {
		t.Fatalf("unexpected fallback identity: kind=%q scope=%q subkey=%q", record.IdentityKind, record.IdentityScope, record.IdentitySubkey)
	}
	if record.TimestampMs != 1_700_000_007_000 {
		t.Fatalf("expected step timestamp, got %d", record.TimestampMs)
	}
	if record.NativeSessionID != session {
		t.Fatalf("expected file-stem session id, got %q", record.NativeSessionID)
	}
}

func TestAntigravitySkipsNonConversationDBs(t *testing.T) {
	// A summaries-style database has no conversation tables and is skipped
	// silently.
	quiet := filepath.Join(t.TempDir(), "conversation_summaries.db")
	conn, err := sql.Open("sqlite3", quiet)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := conn.Exec("CREATE TABLE summaries (id text)"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	conn.Close()
	records, _, err := NewAntigravityAdapter().ParseFileWithWarnings(quiet)
	if err != nil {
		t.Fatalf("expected silent skip, got error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no records, got %d", len(records))
	}

	// Steps without gen_metadata is an unsupported schema and must fail closed.
	stepsOnly := filepath.Join(t.TempDir(), "33333333-0000-0000-0000-000000000000.db")
	antigravityFixtureDB(t, stepsOnly, nil,
		[]antigravityFixtureStep{{idx: 0, timestampMs: 1_700_000_000_000}}, 0)
	// Remove gen_metadata to simulate the partial schema.
	conn, err = sql.Open("sqlite3", stepsOnly)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := conn.Exec("DROP TABLE gen_metadata"); err != nil {
		t.Fatalf("drop gen_metadata: %v", err)
	}
	conn.Close()
	if _, _, err := NewAntigravityAdapter().ParseFileWithWarnings(stepsOnly); err == nil {
		t.Fatal("expected unsupported-schema error for steps without gen_metadata")
	}
}

func TestAntigravitySchemaValidationRejectsMissingColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "44444444-0000-0000-0000-000000000000.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := conn.Exec("CREATE TABLE gen_metadata (idx integer PRIMARY KEY, size integer)"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	conn.Close()
	if _, _, err := NewAntigravityAdapter().ParseFileWithWarnings(path); err == nil {
		t.Fatal("expected missing data column to fail closed")
	}
}

func TestAntigravityIdentityStableAcrossParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "55555555-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{
		inputTokens:         42,
		totalOutputTokens:   12,
		visibleOutputTokens: 12,
		messageID:           "msg-stable",
	}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 0, usage: &usage, modelDisplay: "gemini-4-carbon", modelID: 1427, hasModelID: true}}, nil, 1_700_000_000_000)

	first := antigravityParseFixture(t, path)
	second := antigravityParseFixture(t, path)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 record per parse, got %d and %d", len(first), len(second))
	}
	firstIDs := antigravityIdentityMap(t, first)
	secondIDs := antigravityIdentityMap(t, second)
	for eventID, identity := range firstIDs {
		other, ok := secondIDs[eventID]
		if !ok {
			t.Fatalf("event %s missing on second parse", eventID)
		}
		if identity != other {
			t.Fatalf("event %s identity changed: %+v vs %+v", eventID, identity, other)
		}
	}
}

func antigravityIdentityMap(t *testing.T, records []*fingerprint.ParsedRecord) map[string]fingerprint.Strategy {
	t.Helper()
	identities := make(map[string]fingerprint.Strategy, len(records))
	for _, record := range records {
		sessionKey, eventID, strategy, _, err := fingerprint.ComputeIdentity(record)
		if err != nil {
			t.Fatalf("compute identity: %v", err)
		}
		if sessionKey == "" {
			t.Fatal("expected non-empty session key")
		}
		identities[eventID] = strategy
	}
	return identities
}

func TestAntigravityZeroUsageSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "66666666-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{messageID: "msg-zero"}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 0, usage: &usage}}, nil, 1_700_000_000_000)

	adapter := NewAntigravityAdapter()
	records, _, err := adapter.ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected zero-usage row to be skipped, got %d", len(records))
	}
	found := false
	for _, diagnostic := range adapter.ImportDiagnostics() {
		if diagnostic.Code == "antigravity_skipped_zero_usage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected zero_usage diagnostic, got %+v", adapter.ImportDiagnostics())
	}
}

func TestAntigravityRegisteredInDefaults(t *testing.T) {
	registered := false
	for _, adapter := range AllAdapters() {
		if adapter.Name() == "antigravity" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("expected antigravity adapter to be registered")
	}
	agentCfg := config.Default().Agents.Antigravity
	if !agentCfg.Enabled || len(agentCfg.Paths) != 6 {
		t.Fatalf("expected antigravity defaults with 6 roots, got %+v", agentCfg)
	}
	for _, path := range agentCfg.Paths {
		if !strings.Contains(path, "conversations") {
			t.Fatalf("expected conversation root, got %q", path)
		}
	}
}

func TestAntigravityProviderFallbackForUnknownModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "77777777-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{inputTokens: 1, totalOutputTokens: 1, visibleOutputTokens: 1, messageID: "msg-unknown"}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 0, usage: &usage}}, nil, 1_700_000_000_000)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Model != "gemini-internal-model" || !records[0].ModelIsFallback {
		t.Fatalf("expected internal default model, got %+v", records[0])
	}
	// Without model evidence the adapter leaves the provider empty; the
	// import layer fills it as unknown instead of guessing a vendor.
	if records[0].Provider != "" {
		t.Fatalf("expected empty provider for default model, got %q", records[0].Provider)
	}
}

func TestAntigravityPackedAndRepeatedStepIndices(t *testing.T) {
	session := "88888888-0000-0000-0000-000000000000"
	path := filepath.Join(t.TempDir(), session+".db")
	usage := antigravityFixtureUsage{inputTokens: 9, totalOutputTokens: 9, visibleOutputTokens: 9}
	// A gen row linked to two steps merges with both step mirrors.
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 0, usage: &usage, stepIndices: []int64{1, 2}}},
		[]antigravityFixtureStep{
			{idx: 1, usage: &usage, timestampMs: 1_700_000_001_000},
			{idx: 2, usage: &usage, timestampMs: 1_700_000_002_000},
		},
		0,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected multi-step mirror to merge into 1 record, got %d", len(records))
	}
	if records[0].InputTokens != 9 {
		t.Fatalf("unexpected tokens: %+v", records[0])
	}
	if !strings.HasPrefix(records[0].IdentitySubkey, "step:") {
		t.Fatalf("expected step fallback subkey, got %q", records[0].IdentitySubkey)
	}
}

func TestAntigravityMalformedBlobFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "99999999-0000-0000-0000-000000000000.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := conn.Exec(antigravityFixtureSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec("INSERT INTO gen_metadata (idx, data, size) VALUES (0, ?, 3)", []byte{0x0a, 0xff}); err != nil {
		t.Fatalf("insert malformed blob: %v", err)
	}
	conn.Close()
	if _, _, err := NewAntigravityAdapter().ParseFileWithWarnings(path); err == nil {
		t.Fatal("expected malformed gen_metadata blob to fail closed")
	}
}

func TestAntigravityNamedModelFromStepMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aaaa1111-0000-0000-0000-000000000000.db")
	usage := antigravityFixtureUsage{inputTokens: 3, totalOutputTokens: 3, visibleOutputTokens: 3, messageID: "msg-step-model"}
	antigravityFixtureDB(t, path,
		nil,
		[]antigravityFixtureStep{{
			idx:            0,
			usage:          &usage,
			modelInfoName:  "gemini 3 pro",
			modelInfoID:    246,
			hasModelInfoID: true,
			timestampMs:    1_700_000_000_000,
		}},
		0,
	)

	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ModelNormalized != "gemini-3-pro" {
		t.Fatalf("expected step model info to resolve the model, got %q", records[0].ModelNormalized)
	}
	if fmt.Sprint(records[0].TotalTokens) != "6" {
		t.Fatalf("unexpected total: %d", records[0].TotalTokens)
	}
}

func TestAntigravityMixedIdentityUsesLinkedStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	gen := antigravityFixtureUsage{inputTokens: 10, totalOutputTokens: 100, messageID: "synthetic-message"}
	step := antigravityFixtureUsage{inputTokens: 10, totalOutputTokens: 100, visibleOutputTokens: 60, reasoningTokens: 40}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 0, usage: &gen, stepIndices: []int64{5}}},
		[]antigravityFixtureStep{{idx: 5, usage: &step, timestampMs: 1700000050000}},
		1700000000000,
	)
	records := antigravityParseFixture(t, path)
	if len(records) != 1 {
		t.Fatalf("wanted one linked request, got %d", len(records))
	}
	record := records[0]
	if record.NativeEventID != "synthetic-message" || record.TimestampMs != 1700000050000 ||
		record.TotalTokens != 110 || record.OutputTokens != 60 || record.ReasoningTokens != 40 {
		t.Fatalf("wrong linked usage: %+v", record)
	}
}

func TestAntigravityUnkeyedRetriesKeepDistinctRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	a := antigravityFixtureUsage{inputTokens: 12}
	b := antigravityFixtureUsage{inputTokens: 23}
	c := antigravityFixtureUsage{inputTokens: 34}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 1, usage: &a, retries: []antigravityFixtureUsage{a, b, c}}},
		nil, 1700000000000)
	records := antigravityParseFixture(t, path)
	if len(records) != 3 || len(antigravityIdentityMap(t, records)) != 3 {
		t.Fatalf("wanted three distinct records, got %d", len(records))
	}
}

func TestAntigravityScopesModelAndTimestampToLinkedRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	first := antigravityFixtureUsage{inputTokens: 10, messageID: "synthetic-first"}
	second := antigravityFixtureUsage{inputTokens: 10, messageID: "synthetic-second"}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{
			{idx: 1, usage: &first, timestampMs: 1700000001000, modelDisplay: "gemini-4-carbon", stepIndices: []int64{5}},
			{idx: 2, usage: &second, modelDisplay: "gemini-4-hydrogen", stepIndices: []int64{6}},
		},
		[]antigravityFixtureStep{
			{idx: 5, usage: &first, timestampMs: 1700000000000},
			{idx: 6, usage: &second, timestampMs: 1700000002000},
		}, 0)
	records := antigravityParseFixture(t, path)
	if len(records) != 2 || records[0].ModelNormalized != "gemini-4-carbon" ||
		records[0].TimestampMs != 1700000001000 || records[1].ModelNormalized != "gemini-4-hydrogen" {
		t.Fatalf("unexpected requests: %+v", records)
	}
}

func TestAntigravityOpensRelativePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	usage := antigravityFixtureUsage{inputTokens: 10}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 1, usage: &usage}}, nil, 1700000000000)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	records := antigravityParseFixture(t, relative)
	if len(records) != 1 {
		t.Fatalf("expected the relative path to yield a record, got %d", len(records))
	}
}

func TestAntigravityMalformedTrajectoryFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	usage := antigravityFixtureUsage{inputTokens: 10}
	antigravityFixtureDB(t, path, []antigravityFixtureGen{{idx: 1, usage: &usage, timestampMs: 1700000000000}}, nil, 0)
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("INSERT INTO trajectory_metadata_blob (data) VALUES (?)", []byte{0x12, 0x80}); err != nil {
		t.Fatal(err)
	}
	records, err := NewAntigravityAdapter().ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "trajectory metadata") || len(records) != 0 {
		t.Fatalf("expected rejection of malformed trajectory, records=%d err=%v", len(records), err)
	}
}

func TestAntigravityReadTransactionSeesOneSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.db")
	old := antigravityFixtureUsage{inputTokens: 10, messageID: "synthetic-before"}
	replacement := antigravityFixtureUsage{inputTokens: 20, messageID: "synthetic-after"}
	antigravityFixtureDB(t, path,
		[]antigravityFixtureGen{{idx: 1, usage: &old, timestampMs: 1700000000000}}, nil, 0)
	writer, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec("PRAGMA journal_mode = WAL"); err != nil {
		t.Fatal(err)
	}
	reader, err := openAntigravityReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	snapshot, err := reader.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	gens, err := readAntigravityGenerationRows(snapshot)
	if err != nil || len(gens) != 1 || gens[0].metadata.usage.messageID != "synthetic-before" {
		t.Fatalf("generation before commit: %+v %v", gens, err)
	}

	update, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer update.Rollback()
	gen := antigravityFixtureGen{idx: 1, usage: &replacement, timestampMs: 1700000000000}
	if _, err := update.Exec("UPDATE gen_metadata SET data = ? WHERE idx = 1", gen.blob()); err != nil {
		t.Fatal(err)
	}
	step := antigravityFixtureStep{idx: 5, usage: &replacement, timestampMs: 1700000000000}
	if _, err := update.Exec("INSERT INTO steps (idx, metadata) VALUES (5, ?)", step.blob()); err != nil {
		t.Fatal(err)
	}
	if err := update.Commit(); err != nil {
		t.Fatal(err)
	}

	steps, err := readAntigravityStepRows(snapshot)
	if err != nil || len(steps) != 0 {
		t.Fatalf("snapshot picked up later steps: %d %v", len(steps), err)
	}
	records := antigravityParseFixture(t, path)
	if len(records) != 1 || records[0].NativeEventID != "synthetic-after" {
		t.Fatalf("next snapshot has unexpected records: %+v", records)
	}
}
