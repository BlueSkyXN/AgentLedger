package adapters

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

const zcodeFixtureSchema = `
CREATE TABLE session (
	id text primary key,
	path text,
	directory text not null
);
CREATE TABLE model_usage (
	id text primary key,
	logical_request_id text not null,
	attempt_index integer not null default 0,
	session_id text not null references session(id) on delete cascade,
	turn_id text,
	assistant_message_id text,
	query_source text not null,
	provider_id text not null,
	model_id text not null,
	status text not null,
	started_at integer not null,
	input_tokens integer not null default 0,
	output_tokens integer not null default 0,
	reasoning_tokens integer not null default 0,
	cache_creation_input_tokens integer not null default 0,
	cache_read_input_tokens integer not null default 0,
	provider_total_tokens integer,
	computed_total_tokens integer not null default 0
);
`

type zcodeFixtureRow struct {
	id            string
	attempt       int64
	session       string
	querySource   string
	model         string
	status        string
	startedAt     int64
	input         int64
	output        int64
	reasoning     int64
	cacheCreate   int64
	cacheRead     int64
	providerTotal *int64
}

func zcodeInsertRow(t *testing.T, conn *sql.DB, row zcodeFixtureRow) {
	t.Helper()
	var providerTotal any
	if row.providerTotal != nil {
		providerTotal = *row.providerTotal
	}
	_, err := conn.Exec(`INSERT INTO model_usage (
		id, logical_request_id, attempt_index, session_id, turn_id, assistant_message_id,
		query_source, provider_id, model_id, status, started_at,
		input_tokens, output_tokens, reasoning_tokens,
		cache_creation_input_tokens, cache_read_input_tokens,
		provider_total_tokens, computed_total_tokens
	) VALUES (?, ?, ?, ?, ?, ?, ?, 'builtin:fixture-plan', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.id, "logical-"+row.id, row.attempt, row.session, "turn-"+row.session, "msg-"+row.id,
		row.querySource, row.model, row.status, row.startedAt,
		row.input, row.output, row.reasoning, row.cacheCreate, row.cacheRead,
		providerTotal, row.input+row.output)
	if err != nil {
		t.Fatalf("insert fixture row %s: %v", row.id, err)
	}
}

func zcodeInsertSession(t *testing.T, conn *sql.DB, id string, path any, directory string) {
	t.Helper()
	if _, err := conn.Exec(`INSERT INTO session (id, path, directory) VALUES (?, ?, ?)`, id, path, directory); err != nil {
		t.Fatalf("insert fixture session %s: %v", id, err)
	}
}

// writeZCodeFixtureDatabase creates a synthetic ZCode CLI database. All rows
// are synthetic; no real session ids, paths, or usage values are embedded.
func writeZCodeFixtureDatabase(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(zcodeFixtureSchema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	return path
}

func TestZCodeDiscoverFindsDatabasesOnly(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "cli", "db")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeZCodeFixtureDatabase(t, nested, "db.sqlite")
	if err := os.WriteFile(filepath.Join(nested, "README.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, "legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "tasks-index.sqlite"), []byte("not-a-target"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapter := NewZCodeAdapter()
	files, err := adapter.Discover([]string{root})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(filepath.ToSlash(files[0]), "cli/db/db.sqlite") {
		t.Fatalf("expected only the nested db.sqlite, got %v", files)
	}

	explicit, err := adapter.Discover([]string{filepath.Join(nested, "db.sqlite")})
	if err != nil {
		t.Fatalf("discover explicit file: %v", err)
	}
	if len(explicit) != 1 || filepath.Clean(explicit[0]) != filepath.Join(nested, "db.sqlite") {
		t.Fatalf("explicit file discovery failed: %v", explicit)
	}

	missing, err := adapter.Discover([]string{filepath.Join(root, "does-not-exist")})
	if err != nil {
		t.Fatalf("discover missing path must not error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing path must yield no files, got %v", missing)
	}
}

func TestZCodeParsesRequestUsageFromModelUsage(t *testing.T) {
	path := writeZCodeFixtureDatabase(t, t.TempDir(), "db.sqlite")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	zcodeInsertSession(t, conn, "session-1", "/private/project-one", "/ignored-directory")
	zcodeInsertSession(t, conn, "session-2", nil, "/fallback/project-two")
	providerTotal := int64(1100)
	zcodeInsertRow(t, conn, zcodeFixtureRow{
		id: "usage-1", session: "session-1", querySource: "main_turn",
		model: "GLM-5.3", status: "completed", startedAt: 1788775742402,
		input: 1000, output: 100, reasoning: 40, cacheCreate: 3, cacheRead: 60,
		providerTotal: &providerTotal,
	})
	zcodeInsertRow(t, conn, zcodeFixtureRow{
		id: "usage-2", session: "session-2", querySource: "subagent",
		model: "gpt-5.6-sol", status: "completed", startedAt: 1788775742402,
		input: 200, output: 10,
	})
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	adapter := NewZCodeAdapter()
	records, warnings, err := adapter.ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(records) != 2 {
		t.Fatalf("expected two records, got %d", len(records))
	}

	first := records[0]
	if first.Agent != "zcode" || first.SourceProduct != "zcode-cli-db" {
		t.Fatalf("unexpected source identity: %#v", first)
	}
	if first.NativeEventID != "usage-1" || first.IdentityKind != "request" || first.RequestID != "logical-usage-1" {
		t.Fatalf("unexpected identity fields: %#v", first)
	}
	if first.SessionID != "session-1" || first.NativeSessionID != "session-1" || first.ProjectPath != "/private/project-one" {
		t.Fatalf("unexpected session/project fields: %#v", first)
	}
	if first.TurnID != "turn-session-1" || first.MessageID != "msg-usage-1" {
		t.Fatalf("unexpected turn/message fields: %#v", first)
	}
	if first.InputTokens != 937 || first.CacheCreationTokens != 3 || first.CacheReadTokens != 60 {
		t.Fatalf("cached input must be split out of raw input: %#v", first)
	}
	if first.OutputTokens != 100 || first.ReasoningTokens != 40 || first.TotalTokens != 1100 {
		t.Fatalf("unexpected token totals: %#v", first)
	}
	if first.RawInputTokens == nil || *first.RawInputTokens != 1000 || first.SourceTotalTokens == nil || *first.SourceTotalTokens != 1100 {
		t.Fatalf("unexpected source diagnostics: %#v", first)
	}
	if first.ParserVersion != zcodeParserVersion || first.Granularity != "request" || first.IdentityScope != "session" {
		t.Fatalf("unexpected parser metadata: %#v", first)
	}
	if first.TokenAccountingMethod != model.AccZCodeModelUsage || first.AccountingProfile != zcodeAccountingProfile {
		t.Fatalf("unexpected accounting metadata: %#v", first)
	}
	if first.Provider != "zai" {
		t.Fatalf("glm models must map to the zai provider label, got %q", first.Provider)
	}
	if first.TimestampMs != 1788775742402 {
		t.Fatalf("unexpected timestamp: %d", first.TimestampMs)
	}

	second := records[1]
	if second.ProjectPath != "/fallback/project-two" {
		t.Fatalf("session.path must fall back to directory: %#v", second)
	}
	if second.Provider != "openai" {
		t.Fatalf("gpt models must map to the openai provider label, got %q", second.Provider)
	}
	if second.TotalTokens != 210 {
		t.Fatalf("missing provider total must fall back to computed total: %#v", second)
	}

	for _, record := range records {
		if _, _, strategy, _, err := fingerprint.ComputeIdentity(record); err != nil || strategy != fingerprint.StrategyNativeRequest {
			t.Fatalf("identity must resolve to a native request identity: strategy=%v err=%v", strategy, err)
		}
	}

	assertZCodeEnvelopeIsPrivate(t, first.FingerprintJSON)
}

func assertZCodeEnvelopeIsPrivate(t *testing.T, envelope string) {
	t.Helper()
	lower := strings.ToLower(envelope)
	for _, forbidden := range []string{"raw_usage", "provider_metadata", "error_message", "provider_id", "trace_id", "span_id", "content"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("fingerprint envelope leaks %q: %s", forbidden, envelope)
		}
	}
}

func TestZCodeAttemptsAndIdentityStability(t *testing.T) {
	dirOne := t.TempDir()
	pathOne := writeZCodeFixtureDatabase(t, dirOne, "db.sqlite")
	conn, err := sql.Open("sqlite3", pathOne)
	if err != nil {
		t.Fatal(err)
	}
	zcodeInsertSession(t, conn, "session-1", "/private/project", "/private/project")
	conn.Exec(`INSERT INTO model_usage (
		id, logical_request_id, attempt_index, session_id, query_source, provider_id,
		model_id, status, started_at, input_tokens, output_tokens, computed_total_tokens
	) VALUES ('attempt-0', 'same-logical', 0, 'session-1', 'main_turn', 'builtin:fixture-plan',
		'GLM-5.3', 'error', 1788775742402, 100, 10, 110)`)
	conn.Exec(`INSERT INTO model_usage (
		id, logical_request_id, attempt_index, session_id, query_source, provider_id,
		model_id, status, started_at, input_tokens, output_tokens, computed_total_tokens
	) VALUES ('attempt-1', 'same-logical', 1, 'session-1', 'main_turn', 'builtin:fixture-plan',
		'GLM-5.3', 'completed', 1788775742403, 300, 20, 320)`)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	adapter := NewZCodeAdapter()
	records, err := adapter.ParseFile(pathOne)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("retries must stay separate events, got %d", len(records))
	}
	if records[0].NativeEventID == records[1].NativeEventID {
		t.Fatal("attempt rows collapsed into one event")
	}
	if records[0].RequestID != records[1].RequestID {
		t.Fatalf("attempt rows must keep the shared logical request id: %#v", records)
	}

	firstEventID, err := zcodeEventID(t, records[0])
	if err != nil {
		t.Fatal(err)
	}

	// The same database relocated to another path must yield identical event
	// identities: source location is a diagnostic, never part of identity.
	pathTwo := filepath.Join(t.TempDir(), "moved.sqlite")
	if err := copyFile(pathOne, pathTwo); err != nil {
		t.Fatal(err)
	}
	moved, err := adapter.ParseFile(pathTwo)
	if err != nil {
		t.Fatalf("parse moved database: %v", err)
	}
	if len(moved) != 2 {
		t.Fatalf("moved database must parse identically, got %d records", len(moved))
	}
	movedEventID, err := zcodeEventID(t, moved[0])
	if err != nil {
		t.Fatal(err)
	}
	if firstEventID != movedEventID {
		t.Fatalf("event identity changed after database relocation: %s != %s", firstEventID, movedEventID)
	}
}

func zcodeEventID(t *testing.T, record *fingerprint.ParsedRecord) (string, error) {
	t.Helper()
	_, eventID, _, _, err := fingerprint.ComputeIdentity(record)
	return eventID, err
}

func TestZCodeSkipsUnusableRows(t *testing.T) {
	path := writeZCodeFixtureDatabase(t, t.TempDir(), "db.sqlite")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	zcodeInsertSession(t, conn, "session-1", "/private/project", "/private/project")
	mismatch := int64(9999)
	rows := []zcodeFixtureRow{
		{id: "zero-completed", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "completed", startedAt: 1788775742402},
		{id: "zero-running", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "running", startedAt: 1788775742402},
		{id: "cache-exceeds-input", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "completed", startedAt: 1788775742402, input: 10, output: 5, cacheRead: 20},
		{id: "reasoning-exceeds-output", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "completed", startedAt: 1788775742402, input: 10, output: 5, reasoning: 7},
		{id: "provider-total-mismatch", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "completed", startedAt: 1788775742402, input: 10, output: 5, providerTotal: &mismatch},
		{id: "negative-output", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "error", startedAt: 1788775742402, input: 10, output: -5},
	}
	for _, row := range rows {
		zcodeInsertRow(t, conn, row)
	}
	// A row whose computed total disagrees with its components, written
	// directly because the helper derives the computed total.
	if _, err := conn.Exec(`INSERT INTO model_usage (
		id, logical_request_id, session_id, query_source, provider_id, model_id, status, started_at,
		input_tokens, output_tokens, computed_total_tokens
	) VALUES ('computed-mismatch', 'logical-computed-mismatch', 'session-1', 'main_turn', 'builtin:fixture-plan',
		'GLM-5.3', 'completed', 1788775742402, 10, 5, 99)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO model_usage (
		id, logical_request_id, session_id, query_source, provider_id, model_id, status, started_at,
		input_tokens, output_tokens, computed_total_tokens
	) VALUES ('missing-model', 'logical-missing-model', 'session-1', 'main_turn', 'builtin:fixture-plan',
		'', 'completed', 1788775742402, 10, 5, 15)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	adapter := NewZCodeAdapter()
	records, warnings, err := adapter.ParseFileWithWarnings(path)
	if err != nil {
		t.Fatalf("unusable rows must not fail the whole database: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no records from unusable rows, got %d", len(records))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one aggregated warning, got %v", warnings)
	}
	diagnosticCodes := make(map[string]int64)
	for _, diagnostic := range adapter.ImportDiagnostics() {
		diagnosticCodes[diagnostic.Code] = diagnostic.Count
	}
	expected := map[string]int64{
		"zcode_skipped_zero_usage":              2,
		"zcode_skipped_invalid_token_details":   2,
		"zcode_skipped_invalid_token_totals":    2,
		"zcode_skipped_invalid_source_total":    1,
		"zcode_skipped_missing_required_fields": 1,
	}
	for code, count := range expected {
		if diagnosticCodes[code] != count {
			t.Fatalf("diagnostic %s = %d, want %d (all: %v)", code, diagnosticCodes[code], count, diagnosticCodes)
		}
	}
}

func TestZCodeImportsTerminalStatusesWithUsage(t *testing.T) {
	path := writeZCodeFixtureDatabase(t, t.TempDir(), "db.sqlite")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	zcodeInsertSession(t, conn, "session-1", "/private/project", "/private/project")
	zcodeInsertRow(t, conn, zcodeFixtureRow{id: "failed-call", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "error", startedAt: 1788775742402, input: 120, output: 0})
	zcodeInsertRow(t, conn, zcodeFixtureRow{id: "cancelled-call", session: "session-1", querySource: "main_turn", model: "GLM-5.3", status: "cancelled", startedAt: 1788775742403, input: 80, output: 4})
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	records, err := NewZCodeAdapter().ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("terminal statuses with real usage must be imported, got %d", len(records))
	}
	if records[0].TotalTokens != 120 || records[0].OutputTokens != 0 {
		t.Fatalf("error rows keep their consumed input tokens: %#v", records[0])
	}
}

func TestZCodeRejectsUnsupportedSchema(t *testing.T) {
	dir := t.TempDir()

	missingTable := filepath.Join(dir, "empty.sqlite")
	conn, err := sql.Open("sqlite3", missingTable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE session (id text primary key, path text, directory text not null)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = NewZCodeAdapter().ParseFile(missingTable)
	if err == nil || !strings.Contains(err.Error(), "unsupported ZCode schema") {
		t.Fatalf("missing model_usage must fail closed, got %v", err)
	}

	missingColumn := filepath.Join(dir, "partial.sqlite")
	conn, err = sql.Open("sqlite3", missingColumn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE session (id text primary key, path text, directory text not null)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE model_usage (id text primary key, session_id text not null, started_at integer not null)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = NewZCodeAdapter().ParseFile(missingColumn)
	if err == nil || !strings.Contains(err.Error(), "missing column") {
		t.Fatalf("schema drift must fail closed with the missing column, got %v", err)
	}
}

func TestZCodeProviderForModel(t *testing.T) {
	cases := map[string]string{
		"GLM-5.3":                  "zai",
		"glm-5.3-flash":            "zai",
		"origin-deepseek-v4-flash": "deepseek",
		"grok-4.6":                 "xai",
		"gemini-3.8-flash":         "google",
		"gpt-5.6-sol":              "openai",
		"claude-opus-4":            "anthropic",
		"totally-unknown-model":    "",
	}
	for modelID, provider := range cases {
		if got := zcodeProviderForModel(modelID); got != provider {
			t.Fatalf("zcodeProviderForModel(%q) = %q, want %q", modelID, got, provider)
		}
	}
}

func TestZCodeLiveCorpus(t *testing.T) {
	if os.Getenv("ZCODE_LIVE_TEST") != "1" {
		t.Skip("set ZCODE_LIVE_TEST=1 to validate the local ZCode corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	adapter := NewZCodeAdapter()
	files, err := adapter.Discover([]string{filepath.Join(home, ".zcode", "cli", "db")})
	if err != nil {
		t.Fatalf("discover live corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no ZCode database discovered")
	}

	var events int64
	var total, input, output int64
	sessions := make(map[string]struct{})
	models := make(map[string]struct{})
	for _, file := range files {
		records, err := adapter.ParseFile(file)
		if err != nil {
			t.Fatalf("parse live ZCode database: %v", err)
		}
		for _, rec := range records {
			events++
			total += rec.TotalTokens
			input += rec.InputTokens + rec.CacheCreationTokens + rec.CacheReadTokens
			output += rec.OutputTokens
			sessions[rec.NativeSessionID] = struct{}{}
			models[rec.Model] = struct{}{}
			if rec.Agent != "zcode" || rec.SourceProduct != "zcode-cli-db" || rec.NativeEventID == "" || rec.NativeSessionID == "" {
				t.Fatalf("invalid live ZCode record metadata")
			}
			if rec.TotalTokens != rec.InputTokens+rec.OutputTokens+rec.CacheCreationTokens+rec.CacheReadTokens {
				t.Fatalf("live ZCode record violates token conservation: %#v", rec)
			}
			if rec.InputTokens < 0 || rec.OutputTokens < 0 || rec.ReasoningTokens > rec.OutputTokens {
				t.Fatalf("live ZCode record violates token invariants: %#v", rec)
			}
		}
	}
	fmt.Printf("zcode live corpus: events=%d sessions=%d models=%d input=%d output=%d total=%d\n",
		events, len(sessions), len(models), input, output, total)
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o644)
}
