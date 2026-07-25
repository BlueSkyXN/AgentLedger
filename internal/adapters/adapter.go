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
