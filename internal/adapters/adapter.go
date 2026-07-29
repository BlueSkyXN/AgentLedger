package adapters

import (
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
)

// Adapter is the interface for source log parsers
type Adapter interface {
	Name() string
	Discover(paths []string) ([]string, error)
	ParseFile(path string) ([]*fingerprint.ParsedRecord, error)
}

// ParseWarningAdapter can return non-fatal, aggregated diagnostics while still
// importing valid records from the same file.
type ParseWarningAdapter interface {
	ParseFileWithWarnings(path string) ([]*fingerprint.ParsedRecord, []string, error)
}

// FileSetPreparer lets an adapter inspect relationships between a stable set
// of source files before the files are parsed individually.
type FileSetPreparer interface {
	PrepareFileSet(paths []string) error
}

// ImportDiagnostic is an aggregate, privacy-safe adapter import diagnostic.
type ImportDiagnosticUnit string

const (
	ImportDiagnosticUnitCount  ImportDiagnosticUnit = "count"
	ImportDiagnosticUnitEvents ImportDiagnosticUnit = "events"
	ImportDiagnosticUnitTokens ImportDiagnosticUnit = "tokens"
	ImportDiagnosticUnitUsage  ImportDiagnosticUnit = "usage"
)

type ImportDiagnostic struct {
	Code   string
	Unit   ImportDiagnosticUnit
	Count  int64
	Events int64
	Tokens int64
}

// ImportDiagnosticsProvider exposes aggregate adapter diagnostics after parsing.
type ImportDiagnosticsProvider interface {
	ImportDiagnostics() []ImportDiagnostic
}

// RecordPostProcessor can normalize or deduplicate records after all files for
// an adapter have been parsed.
type RecordPostProcessor interface {
	PostProcessRecords(records []*fingerprint.ParsedRecord) []*fingerprint.ParsedRecord
}

// AllAdapters returns all available adapters
func AllAdapters() []Adapter {
	return []Adapter{
		NewClaudeAdapter(),
		NewCodexAdapter(),
		NewGeminiAdapter(),
		NewCopilotAdapter(),
		NewWorkBuddyAdapter(),
	}
}
