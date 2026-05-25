package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiscoverFiles walks directories finding files with given extensions
func DiscoverFiles(basePaths []string, extensions []string) ([]string, error) {
	var files []string
	extSet := make(map[string]bool)
	for _, ext := range extensions {
		extSet[ext] = true
	}

	for _, base := range basePaths {
		expanded := expandHome(base)
		err := filepath.Walk(expanded, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if extSet[ext] {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			continue
		}
	}
	return files, nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func parseTimestamp(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return normalizeEpoch(int64(v))
	case int64:
		return normalizeEpoch(v)
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err == nil {
			return t.UnixMilli()
		}
		t, err = time.Parse(time.RFC3339Nano, v)
		if err == nil {
			return t.UnixMilli()
		}
		return 0
	default:
		return 0
	}
}

func normalizeEpoch(value int64) int64 {
	if value < 0 {
		return 0
	}
	// If less than year 2100 in seconds, assume seconds
	if value < 4_102_444_800 {
		return value * 1000
	}
	// If less than year 2100 in millis, assume millis
	if value < 4_102_444_800_000 {
		return value
	}
	// Assume microseconds
	return value / 1000
}

func getString(obj map[string]interface{}, key string) string {
	if v, ok := obj[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt64(obj map[string]interface{}, key string) int64 {
	if v, ok := obj[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case json.Number:
			i, _ := n.Int64()
			return i
		}
	}
	return 0
}

func getFloat64(obj map[string]interface{}, key string) float64 {
	if v, ok := obj[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func getMap(obj map[string]interface{}, key string) map[string]interface{} {
	if v, ok := obj[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// NormalizeModelName standardizes model names
func NormalizeModelName(raw string) (normalized, provider, family string) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)

	switch {
	case strings.Contains(lower, "claude"):
		provider = "anthropic"
		family = "claude"
	case strings.Contains(lower, "gpt"):
		provider = "openai"
		family = "gpt"
	case strings.Contains(lower, "o1") || strings.Contains(lower, "o3") || strings.Contains(lower, "o4"):
		provider = "openai"
		family = "o-series"
	case strings.Contains(lower, "gemini"):
		provider = "google"
		family = "gemini"
	case strings.Contains(lower, "qwen"):
		provider = "alibaba"
		family = "qwen"
	case strings.Contains(lower, "codex"):
		provider = "openai"
		family = "codex"
	default:
		provider = "unknown"
		family = "unknown"
	}

	normalized = raw
	return
}
