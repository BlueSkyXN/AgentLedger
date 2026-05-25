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

type CodexAdapter struct{}

func NewCodexAdapter() *CodexAdapter {
	return &CodexAdapter{}
}

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.codex"}
	}
	return DiscoverFiles(paths, []string{".jsonl"})
}

func (a *CodexAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
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

		// Codex format: type=event_msg, payload.type=token_count, payload.info.total_token_usage
		usage := getMap(obj, "usage")
		if usage == nil {
			resp := getMap(obj, "response")
			if resp != nil {
				usage = getMap(resp, "usage")
			}
		}
		if usage == nil {
			payload := getMap(obj, "payload")
			if payload != nil && getString(payload, "type") == "token_count" {
				info := getMap(payload, "info")
				if info != nil {
					// Use last_token_usage (per-message), not total_token_usage (cumulative)
					usage = getMap(info, "last_token_usage")
					if usage == nil {
						usage = getMap(info, "total_token_usage")
					}
				}
			}
		}
		if usage == nil {
			continue
		}

		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)

		sessionID := extractCodexSession(path)
		model := getString(obj, "model")
		if model == "" {
			resp := getMap(obj, "response")
			if resp != nil {
				model = getString(resp, "model")
			}
		}
		if model == "" {
			// Try payload.info.model or infer from session filename
			payload := getMap(obj, "payload")
			if payload != nil {
				info := getMap(payload, "info")
				if info != nil {
					model = getString(info, "model")
				}
			}
		}

		rec := &fingerprint.ParsedRecord{
			Agent:           "codex",
			Provider:        "openai",
			Model:           model,
			TimestampMs:     parseTimestamp(obj["timestamp"]),
			SessionID:       sessionID,
			MessageID:       getString(obj, "id"),
			RequestID:       getString(obj, "request_id"),
			InputTokens:     getInt64(usage, "input_tokens") + getInt64(usage, "prompt_tokens"),
			OutputTokens:    getInt64(usage, "output_tokens") + getInt64(usage, "completion_tokens"),
			ReasoningTokens: getInt64(usage, "reasoning_tokens") + getInt64(usage, "reasoning_output_tokens"),
			TotalTokens:     getInt64(usage, "total_tokens"),
			RawJSON:         string(rawJSON),
			SourceFile:      path,
			LineNumber:      lineNum,
			RawSHA256:       rawHash,
		}

		records = append(records, rec)
	}

	return records, scanner.Err()
}

func extractCodexSession(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
