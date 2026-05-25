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

// AllAdapters returns all available adapters
func AllAdapters() []Adapter {
	return []Adapter{
		NewClaudeAdapter(),
		NewCodexAdapter(),
		NewGeminiAdapter(),
		NewQwenAdapter(),
	}
}
