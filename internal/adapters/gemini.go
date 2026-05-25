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

type GeminiAdapter struct{}

func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{}
}

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) Discover(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"~/.gemini"}
	}
	return DiscoverFiles(paths, []string{".json", ".jsonl"})
}

func (a *GeminiAdapter) ParseFile(path string) ([]*fingerprint.ParsedRecord, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".jsonl" {
		return parseGeminiJSONL(path)
	}
	return parseGeminiJSON(path)
}

func parseGeminiJSON(path string) ([]*fingerprint.ParsedRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		var arr []map[string]interface{}
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, nil
		}
		var records []*fingerprint.ParsedRecord
		for i, item := range arr {
			if rec := parseGeminiObject(item, path, i+1); rec != nil {
				records = append(records, rec)
			}
		}
		return records, nil
	}

	rec := parseGeminiObject(obj, path, 1)
	if rec != nil {
		return []*fingerprint.ParsedRecord{rec}, nil
	}
	return nil, nil
}

func parseGeminiJSONL(path string) ([]*fingerprint.ParsedRecord, error) {
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

		if rec := parseGeminiObject(obj, path, lineNum); rec != nil {
			records = append(records, rec)
		}
	}

	return records, scanner.Err()
}

func parseGeminiObject(obj map[string]interface{}, path string, lineNum int) *fingerprint.ParsedRecord {
	usage := getMap(obj, "usageMetadata")
	if usage == nil {
		resp := getMap(obj, "response")
		if resp != nil {
			usage = getMap(resp, "usageMetadata")
		}
	}
	if usage == nil {
		return nil
	}

	rawJSON, _ := json.Marshal(obj)
	rawHash := sha256Hex(rawJSON)

	return &fingerprint.ParsedRecord{
		Agent:        "gemini",
		Provider:     "google",
		Model:        getString(obj, "model"),
		TimestampMs:  parseTimestamp(obj["timestamp"]),
		SessionID:    getString(obj, "session_id"),
		InputTokens:  getInt64(usage, "promptTokenCount"),
		OutputTokens: getInt64(usage, "candidatesTokenCount"),
		TotalTokens:  getInt64(usage, "totalTokenCount"),
		RawJSON:      string(rawJSON),
		SourceFile:   path,
		LineNumber:   lineNum,
		RawSHA256:    rawHash,
	}
}
