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

type QwenAdapter struct{}

func NewQwenAdapter() *QwenAdapter {
	return &QwenAdapter{}
}

func (a *QwenAdapter) Name() string { return "qwen" }

func (a *QwenAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.qwen"}
	}
	return DiscoverFiles(paths, []string{".jsonl"})
}

func (a *QwenAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var records []*fingerprint.ParsedRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	lineNum := 0
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(filepath.Base(path)))

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

		usage := getMap(obj, "usage")
		if usage == nil {
			continue
		}

		rawJSON, _ := json.Marshal(obj)
		rawHash := sha256Hex(rawJSON)

		rec := &fingerprint.ParsedRecord{
			Agent:        "qwen",
			Provider:     "alibaba",
			Model:        getString(obj, "model"),
			TimestampMs:  parseTimestamp(obj["timestamp"]),
			SessionID:    sessionID,
			MessageID:    getString(obj, "message_id"),
			InputTokens:  getInt64(usage, "input_tokens") + getInt64(usage, "prompt_tokens"),
			OutputTokens: getInt64(usage, "output_tokens") + getInt64(usage, "completion_tokens"),
			TotalTokens:  getInt64(usage, "total_tokens"),
			RawJSON:      string(rawJSON),
			SourceFile:   path,
			LineNumber:   lineNum,
			RawSHA256:    rawHash,
		}

		records = append(records, rec)
	}

	return records, scanner.Err()
}
