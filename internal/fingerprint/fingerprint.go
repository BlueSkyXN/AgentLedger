package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// ParsedRecord contains the parsed fields from a source record.  The fields
// which describe source identity are intentionally separate from the legacy
// diagnostic fields below.  In particular, source_file, line_number and
// raw_sha256 are never used by ComputeIdentity.
type ParsedRecord struct {
	Agent    string
	Provider string
	Model    string
	// ModelNormalized optionally overrides the generic model normalizer at import
	// time. Adapters use it only when their source has an explicit canonical ID.
	ModelNormalized string
	ModelResolution string

	TimestampMs int64

	// NativeSessionID is the source supplied session identifier.  SessionPathID
	// is a stable, source-relative path fallback (never an absolute source path).
	NativeSessionID string
	SessionPathID   string
	// NativeEventID is the strongest source event identifier available.  Its
	// interpretation is given by IdentityKind (event/message/request).
	NativeEventID  string
	IdentityKind   string
	IdentityScope  string
	IdentitySubkey string
	ParserVersion  string
	Granularity    string
	ContentSHA256  string

	SessionID   string
	ProjectPath string
	MessageID   string
	RequestID   string
	TurnID      string

	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	ReasoningTokens     int64
	TotalTokens         int64

	SourceTotalTokens *int64
	RawInputTokens    *int64
	IsSidechain       bool
	UsageSpeed        string

	SourceProduct         string
	ObservabilityLevel    string
	ModelIsFallback       bool
	TokenAccountingMethod string
	AccountingProfile     string

	// FingerprintJSON contains source JSON only while importing. It is not part
	// of v3 identity or content identity and must not be persisted as evidence.
	FingerprintJSON string
	SourceFile      string
	LineNumber      int
	RawSHA256       string
}

// Strategy is the v3 identity precedence selected for an event.
type Strategy string

const (
	StrategyNativeEvent     Strategy = "native_event"
	StrategyNativeMessage   Strategy = "native_message"
	StrategyNativeRequest   Strategy = "native_request"
	StrategySessionTurn     Strategy = "session_turn"
	StrategySessionRecord   Strategy = "session_record"
	StrategyContentFallback Strategy = "content_fallback"
)

var (
	errMissingSession = &IdentityError{Code: "missing_session"}
	errMissingSource  = &IdentityError{Code: "missing_source_product"}
)

type IdentityError struct {
	Code string
}

func (e *IdentityError) Error() string {
	return "identity error: " + e.Code
}

func IdentityErrorCode(err error) string {
	var identityErr *IdentityError
	if errors.As(err, &identityErr) {
		return identityErr.Code
	}
	return "invalid_identity"
}

// ComputeIdentity returns a stable session key, event ID, selected precedence,
// and a content hash.  Only source product, session/event identity fields and
// normalized usage facts participate.  Device, provider/model classification,
// token values (except content fallback), timestamps (except content hash),
// absolute source paths, import time, cost and request/timing diagnostics do not
// participate in event identity.
func ComputeIdentity(rec *ParsedRecord) (sessionKey, eventID string, strategy Strategy, contentSHA256 string, err error) {
	if rec == nil {
		return "", "", "", "", &IdentityError{Code: "nil_record"}
	}
	sourceProduct := strings.TrimSpace(rec.SourceProduct)
	if sourceProduct == "" {
		return "", "", "", "", errMissingSource
	}
	if rec.TimestampMs <= 0 {
		return "", "", "", "", &IdentityError{Code: "invalid_timestamp"}
	}

	sessionSeed := strings.TrimSpace(rec.NativeSessionID)
	if sessionSeed == "" {
		sessionSeed = strings.TrimSpace(rec.SessionPathID)
	}
	if sessionSeed == "" {
		return "", "", "", "", errMissingSession
	}
	if strings.TrimSpace(rec.NativeSessionID) == "" {
		sessionSeed, err = normalizeStableSessionPath(sessionSeed)
		if err != nil {
			return "", "", "", "", err
		}
	}
	sessionKey = hashIdentityTuple("session:v1", sourceProduct, sessionSeed)

	contentSHA256, err = contentHash(rec)
	if err != nil {
		return "", "", "", "", &IdentityError{Code: "invalid_content"}
	}
	if rec.ContentSHA256 != "" {
		// A caller may provide a previously computed value, but only after
		// validating that it is the hash of the current structured facts. This
		// prevents RawSHA256 or a stale source envelope from being reused.
		if rec.ContentSHA256 == contentSHA256 {
			contentSHA256 = rec.ContentSHA256
		}
	}

	scope := normalizeScope(rec.IdentityScope)
	kind := strings.TrimSpace(rec.IdentityKind)
	nativeID := strings.TrimSpace(rec.NativeEventID)
	strategy = StrategyContentFallback

	// Explicit NativeEventID can represent event/message/request identities;
	// IdentityKind disambiguates which strategy it belongs to.
	if nativeID != "" {
		switch strings.ToLower(kind) {
		case "message", "native_message":
			strategy, kind = StrategyNativeMessage, "message"
		case "request", "native_request":
			strategy, kind = StrategyNativeRequest, "request"
		case "event", "native_event":
			strategy, kind = StrategyNativeEvent, "event"
		default:
			strategy, kind = StrategyNativeEvent, firstNonEmpty(kind, "event")
		}
	} else if strings.EqualFold(kind, "record") || strings.EqualFold(kind, "session_record") {
		strategy, kind = StrategySessionRecord, "record"
		// A segment subkey makes multiple records in one session distinct while
		// still remaining independent of source location and import order.
		nativeID = firstNonEmpty(rec.IdentitySubkey, "record")
	} else if strings.EqualFold(kind, "turn") && strings.TrimSpace(rec.TurnID) != "" {
		strategy, kind, nativeID = StrategySessionTurn, "turn", strings.TrimSpace(rec.TurnID)
	} else if messageID := strings.TrimSpace(rec.MessageID); messageID != "" {
		strategy, kind, nativeID = StrategyNativeMessage, "message", messageID
	} else if rec.RequestID != "" {
		strategy, kind, nativeID = StrategyNativeRequest, "request", strings.TrimSpace(rec.RequestID)
	} else if rec.TurnID != "" {
		strategy, kind, nativeID = StrategySessionTurn, "turn", strings.TrimSpace(rec.TurnID)
	} else {
		strategy, kind, nativeID = StrategyContentFallback, "content", contentSHA256
	}

	if strings.TrimSpace(rec.IdentitySubkey) != "" && strategy != StrategySessionRecord {
		// For native identities the subkey carries model/segment information and
		// prevents multiple source segments sharing one native ID from collapsing.
		nativeID = firstNonEmpty(nativeID, contentSHA256)
	}
	subkey := strings.TrimSpace(rec.IdentitySubkey)
	parts := []string{"event:v2", sourceProduct, scope}
	if scope == "session" {
		parts = append(parts, sessionKey)
	}
	parts = append(parts, kind, nativeID, subkey)
	eventID = hashIdentityTuple(parts...)
	return sessionKey, eventID, strategy, contentSHA256, nil
}

// hashIdentityTuple preserves component boundaries. Native identifiers and
// adapter subkeys are source data and may legally contain delimiter bytes, so
// joining them with a sentinel would make distinct tuples collide before the
// cryptographic hash is applied.
func hashIdentityTuple(parts ...string) string {
	encoded, _ := json.Marshal(parts)
	return sha256Hex(string(encoded))
}

func normalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "global", "source", "account":
		return "global"
	default:
		return "session"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// normalizeStableSessionPath accepts only a source-relative path-like ID. An
// absolute path would make a relocation change the identity and is rejected.
func normalizeStableSessionPath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || strings.HasPrefix(raw, "/") || (len(raw) >= 2 && raw[1] == ':') {
		return "", &IdentityError{Code: "unstable_session_path"}
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", &IdentityError{Code: "unstable_session_path"}
	}
	return cleaned, nil
}

func contentHash(rec *ParsedRecord) (string, error) {
	modelName := strings.TrimSpace(rec.Model)
	modelNormalized := strings.TrimSpace(rec.ModelNormalized)
	// Keep the values as integers and use explicit null markers for optional
	// source totals. The resulting structured envelope excludes project/source
	// locators and parser/import metadata.
	envelope := map[string]interface{}{
		"timestamp_ms":            rec.TimestampMs,
		"channel":                 strings.TrimSpace(rec.Agent),
		"source_product":          strings.TrimSpace(rec.SourceProduct),
		"provider":                strings.TrimSpace(rec.Provider),
		"model":                   modelName,
		"model_normalized":        modelNormalized,
		"model_resolution":        strings.TrimSpace(rec.ModelResolution),
		"model_is_fallback":       rec.ModelIsFallback,
		"observability_level":     strings.TrimSpace(rec.ObservabilityLevel),
		"event_granularity":       strings.TrimSpace(rec.Granularity),
		"input_tokens":            rec.InputTokens,
		"output_tokens":           rec.OutputTokens,
		"cache_creation_tokens":   rec.CacheCreationTokens,
		"cache_read_tokens":       rec.CacheReadTokens,
		"reasoning_tokens":        rec.ReasoningTokens,
		"total_tokens":            rec.TotalTokens,
		"token_accounting_method": strings.TrimSpace(rec.TokenAccountingMethod),
		"accounting_profile":      strings.TrimSpace(rec.AccountingProfile),
	}
	if rec.SourceTotalTokens != nil {
		envelope["source_total_tokens"] = *rec.SourceTotalTokens
	} else {
		envelope["source_total_tokens"] = nil
	}
	if rec.RawInputTokens != nil {
		envelope["raw_input_tokens"] = *rec.RawInputTokens
	} else {
		envelope["raw_input_tokens"] = nil
	}
	canonical, err := CanonicalJSONValue(envelope)
	if err != nil {
		return "", err
	}
	return sha256Hex("content:v1|" + canonical), nil
}

// CanonicalJSON canonicalizes exactly one JSON value. Decoder.UseNumber is
// required so the lexical distinction between 1 and 1.0 survives. Object keys
// are sorted recursively; array order is preserved; malformed/trailing values
// are errors.
func CanonicalJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", errors.New("trailing JSON value")
		}
		return "", fmt.Errorf("trailing JSON: %w", err)
	}
	return marshalCanonical(value)
}

// canonicalJSON is kept as a package-local spelling for parser tests and
// callers that used the pre-v3 helper name; unlike stableJSON it reports input
// errors instead of silently returning the raw text.
func canonicalJSON(raw string) (string, error) { return CanonicalJSON(raw) }

// CanonicalJSONValue applies the same stable encoding to an already decoded
// structured value. It is used by contentHash to avoid a lossy marshal/unmarshal
// round trip.
func CanonicalJSONValue(value interface{}) (string, error) {
	return marshalCanonical(value)
}

func marshalCanonical(value interface{}) (string, error) {
	canonical := canonicalize(value)
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sha256Hex(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

func canonicalize(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// encoding/json sorts string map keys itself, but recursively rebuilding
		// keeps this contract explicit and works for nested values as well.
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
