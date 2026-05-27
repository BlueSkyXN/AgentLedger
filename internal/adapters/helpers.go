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

func int64Field(obj map[string]interface{}, key string) (int64, bool) {
	if obj == nil {
		return 0, false
	}
	v, ok := obj[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return i, true
		}
	case string:
		var parsed json.Number = json.Number(strings.TrimSpace(n))
		i, err := parsed.Int64()
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func firstInt64Field(obj map[string]interface{}, keys ...string) (int64, bool) {
	for _, key := range keys {
		if value, ok := int64Field(obj, key); ok {
			return value, true
		}
	}
	return 0, false
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

func getNestedString(obj map[string]interface{}, keys ...string) string {
	current := obj
	for i, key := range keys {
		if i == len(keys)-1 {
			return getString(current, key)
		}
		current = getMap(current, key)
		if current == nil {
			return ""
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func int64Ptr(value int64) *int64 {
	return &value
}

func jsonStringArray(values []string) string {
	values = uniqueStrings(values)
	if len(values) == 0 {
		return ""
	}
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
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
