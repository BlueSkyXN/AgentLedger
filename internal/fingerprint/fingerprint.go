package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ParsedRecord contains the parsed fields from a source record
type ParsedRecord struct {
	Agent               string
	Provider            string
	Model               string
	TimestampMs         int64
	SessionID           string
	ProjectPath         string
	MessageID           string
	RequestID           string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	ReasoningTokens     int64
	TotalTokens         int64
	CostUSD             *float64
	RequestStartedAtMs  int64
	FirstTokenAtMs      int64
	CompletedAtMs       int64
	TotalDurationMs     int64
	TTFTMs              int64
	OutputDurationMs    int64
	RawJSON             string
	SourceFile          string
	LineNumber          int
	RawSHA256           string
}

// Strategy represents the fingerprint strategy used
type Strategy string

const (
	StrategyMessageID    Strategy = "message_id"
	StrategySessionToken Strategy = "session_token"
	StrategyRawHash      Strategy = "raw_hash"
	StrategyFallback     Strategy = "fallback"
)

// Compute computes the event fingerprint using 4-level priority
func Compute(rec *ParsedRecord) (fingerprint string, strategy Strategy) {
	if rec.MessageID != "" {
		hash := sha256Hex(fmt.Sprintf("message_id|%s|%s|%s", rec.Agent, rec.Provider, rec.MessageID))
		return hash, StrategyMessageID
	}

	if rec.SessionID != "" && rec.TimestampMs > 0 {
		hash := sha256Hex(fmt.Sprintf("session_token|%s|%s|%s|%d|%d|%d",
			rec.Agent, rec.Provider, rec.SessionID,
			rec.TimestampMs, rec.InputTokens, rec.OutputTokens))
		return hash, StrategySessionToken
	}

	if rec.RawJSON != "" {
		canonical := stableJSON(rec.RawJSON)
		hash := sha256Hex(fmt.Sprintf("raw_hash|%s|%s|%s", rec.Agent, rec.Provider, canonical))
		return hash, StrategyRawHash
	}

	hash := sha256Hex(fmt.Sprintf("fallback|%s|%d|%s", rec.SourceFile, rec.LineNumber, rec.RawSHA256))
	return hash, StrategyFallback
}

func sha256Hex(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// stableJSON canonicalizes JSON by sorting object keys
func stableJSON(raw string) string {
	var obj interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return raw
	}
	canonical := canonicalize(obj)
	result, err := json.Marshal(canonical)
	if err != nil {
		return raw
	}
	return string(result)
}

func canonicalize(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sorted := make(map[string]interface{}, len(val))
		for _, k := range keys {
			sorted[k] = canonicalize(val[k])
		}
		return sorted
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = canonicalize(item)
		}
		return result
	default:
		return val
	}
}
