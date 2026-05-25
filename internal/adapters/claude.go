package adapters

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
)

type ClaudeAdapter struct{}

func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{}
}

func (a *ClaudeAdapter) Name() string { return "claude" }

func (a *ClaudeAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.claude"}
	}
	return DiscoverFiles(paths, []string{".jsonl"})
}

func (a *ClaudeAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var records []*fingerprint.ParsedRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}

		msgType := getString(obj, "type")
		if msgType != "assistant" {
			continue
		}

		// Usage can be at obj.usage or obj.message.usage
		usage := getMap(obj, "usage")
		if usage == nil {
			msg := getMap(obj, "message")
			if msg != nil {
				usage = getMap(msg, "usage")
			}
		}
		if usage == nil {
			continue
		}

		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)
		sessionID := getString(obj, "sessionId")
		if sessionID == "" {
			sessionID = extractClaudeSession(path)
		}

		// Model can be at obj.model or obj.message.model
		model := getString(obj, "model")
		if model == "" {
			msg := getMap(obj, "message")
			if msg != nil {
				model = getString(msg, "model")
			}
		}

		rec := &fingerprint.ParsedRecord{
			Agent:               "claude",
			Provider:            "anthropic",
			Model:               model,
			TimestampMs:         parseTimestamp(obj["timestamp"]),
			SessionID:           sessionID,
			MessageID:           getString(obj, "uuid"),
			RequestID:           getString(obj, "requestId"),
			InputTokens:         getInt64(usage, "input_tokens"),
			OutputTokens:        getInt64(usage, "output_tokens"),
			CacheCreationTokens: getInt64(usage, "cache_creation_input_tokens"),
			CacheReadTokens:     getInt64(usage, "cache_read_input_tokens"),
			// TotalTokens left as 0 — import.go computes the full sum (incl. cache) consistently with other adapters
			RawJSON:    string(rawJSON),
			SourceFile: path,
			LineNumber: lineNum,
			RawSHA256:  rawHash,
		}

		records = append(records, rec)
	}

	return records, scanner.Err()
}

func extractClaudeSession(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "projects" && i+2 < len(parts) {
			return parts[i+2]
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func extractClaudeProject(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
