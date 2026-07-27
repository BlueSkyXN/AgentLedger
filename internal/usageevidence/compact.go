// Package usageevidence converts recognized local usage envelopes into small,
// deterministic allowlist evidence documents suitable for local persistence.
package usageevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type Status string

const (
	StatusRecognizedLegacy Status = "recognized_legacy"
	StatusAlreadyCompact   Status = "already_compact"
	StatusEmpty            Status = "empty"
	StatusUnknown          Status = "unknown"
	StatusInternalError    Status = "internal_error"
)

const (
	ClaudeSchema = "agentledger.claude-usage.v1"
	CodexSchema  = "agentledger.codex-usage.v1"
)

// Result is the outcome of compacting one raw usage envelope. Err is always a
// generic diagnostic and never includes raw source content.
type Result struct {
	Status Status
	JSON   string
	Err    error
}

// Compact returns compact allowlist evidence for a recognized Claude or Codex
// envelope. Existing compact evidence is normalized deterministically.
func Compact(channel string, raw string) Result {
	if strings.TrimSpace(raw) == "" {
		return Result{Status: StatusEmpty}
	}

	obj, err := decodeObject(raw)
	if err != nil {
		return Result{Status: StatusUnknown}
	}

	if schema := stringValue(obj["schema"]); schema == schemaForChannel(channel) && schema != "" {
		if !validCompact(channel, obj) {
			return Result{Status: StatusUnknown}
		}
		compactJSON, err := marshalDeterministic(obj)
		if err != nil {
			return Result{Status: StatusInternalError, Err: errors.New("usage evidence JSON could not be encoded")}
		}
		return Result{Status: StatusAlreadyCompact, JSON: compactJSON}
	}

	var evidence map[string]interface{}
	switch channel {
	case "claude":
		evidence = compactClaude(obj)
	case "codex":
		evidence = compactCodex(obj)
	default:
		return Result{Status: StatusUnknown}
	}
	if evidence == nil {
		return Result{Status: StatusUnknown}
	}
	if !validCompact(channel, evidence) {
		return Result{Status: StatusUnknown}
	}

	compactJSON, err := marshalDeterministic(evidence)
	if err != nil {
		return Result{Status: StatusInternalError, Err: errors.New("usage evidence JSON could not be encoded")}
	}
	return Result{Status: StatusRecognizedLegacy, JSON: compactJSON}
}

// IsCompact reports whether raw has the matching, supported evidence marker.
func IsCompact(channel string, raw string) bool {
	obj, err := decodeObject(raw)
	return err == nil && stringValue(obj["schema"]) == schemaForChannel(channel) && schemaForChannel(channel) != "" && validCompact(channel, obj)
}

func compactClaude(obj map[string]interface{}) map[string]interface{} {
	if message := objectValue(obj["message"]); message != nil {
		usage := objectValue(obj["usage"])
		if usage == nil {
			usage = objectValue(message["usage"])
		}
		if usage != nil {
			return claudeEvidence("assistant_message", usage, obj, message, obj, stringValue(obj["uuid"]))
		}
	}

	wrapper := objectValue(objectValue(obj["data"])["message"])
	if wrapper == nil {
		return nil
	}
	message := objectValue(wrapper["message"])
	usage := objectValue(message["usage"])
	if message == nil || usage == nil {
		return nil
	}
	return claudeEvidence("agent_progress_wrapped", usage, wrapper, message, wrapper, "")
}

func claudeEvidence(variant string, usage, identitySource, message, costSource map[string]interface{}, uuid string) map[string]interface{} {
	messageIDSource := ""
	if stringValue(message["id"]) != "" {
		messageIDSource = "message_id"
	} else if uuid != "" {
		messageIDSource = "uuid_fallback"
	} else {
		return nil
	}
	evidence := map[string]interface{}{
		"schema":            ClaudeSchema,
		"source_variant":    variant,
		"usage":             usage,
		"is_sidechain":      boolValue(identitySource["isSidechain"]),
		"message_id_source": messageIDSource,
	}
	if cost, ok := costSource["costUSD"]; ok && explicitNumber(cost) {
		evidence["cost_usd"] = cost
	}
	return evidence
}

func compactCodex(obj map[string]interface{}) map[string]interface{} {
	if stringValue(obj["type"]) == "event_msg" {
		payload := objectValue(obj["payload"])
		if stringValue(payload["type"]) == "token_count" {
			info := objectValue(payload["info"])
			if info == nil {
				return nil
			}
			evidence := map[string]interface{}{
				"schema":         CodexSchema,
				"source_variant": "token_count",
			}
			if total := objectValue(info["total_token_usage"]); total != nil {
				evidence["total_token_usage"] = total
			}
			if last := objectValue(info["last_token_usage"]); last != nil {
				evidence["last_token_usage"] = last
			}
			if len(evidence) > 2 {
				return evidence
			}
			return nil
		}
	}

	for _, source := range []struct {
		variant string
		value   map[string]interface{}
	}{
		{variant: "headless_root", value: obj},
		{variant: "headless_data", value: objectValue(obj["data"])},
		{variant: "headless_result", value: objectValue(obj["result"])},
		{variant: "headless_response", value: objectValue(obj["response"])},
	} {
		if usage := objectValue(source.value["usage"]); usage != nil {
			return map[string]interface{}{
				"schema":         CodexSchema,
				"source_variant": source.variant,
				"usage":          usage,
			}
		}
	}
	return nil
}

func schemaForChannel(channel string) string {
	switch channel {
	case "claude":
		return ClaudeSchema
	case "codex":
		return CodexSchema
	default:
		return ""
	}
}

func validCompact(channel string, obj map[string]interface{}) bool {
	switch channel {
	case "claude":
		if !hasOnlyKeys(obj, "schema", "source_variant", "usage", "is_sidechain", "message_id_source", "cost_usd") {
			return false
		}
		usage := objectValue(obj["usage"])
		if !oneOf(stringValue(obj["source_variant"]), "assistant_message", "agent_progress_wrapped") || usage == nil || !validClaudeUsageObject(usage, 0) || !isBool(obj["is_sidechain"]) || !oneOf(stringValue(obj["message_id_source"]), "message_id", "uuid_fallback") {
			return false
		}
		if cost, present := obj["cost_usd"]; present && !explicitNumber(cost) {
			return false
		}
		return true
	case "codex":
		variant := stringValue(obj["source_variant"])
		switch variant {
		case "token_count":
			if !hasOnlyKeys(obj, "schema", "source_variant", "total_token_usage", "last_token_usage") {
				return false
			}
			total := objectValue(obj["total_token_usage"])
			last := objectValue(obj["last_token_usage"])
			return (total != nil || last != nil) && (total == nil || validNumericUsageObject(total, 0)) && (last == nil || validNumericUsageObject(last, 0))
		case "headless_root", "headless_data", "headless_result", "headless_response":
			usage := objectValue(obj["usage"])
			return hasOnlyKeys(obj, "schema", "source_variant", "usage") && usage != nil && validNumericUsageObject(usage, 0)
		default:
			return false
		}
	default:
		return false
	}
}

func validClaudeUsageObject(obj map[string]interface{}, depth int) bool {
	if depth > 4 {
		return false
	}
	for key, value := range obj {
		switch typed := value.(type) {
		case json.Number, bool, nil:
			continue
		case string:
			maxLen := 64
			if key == "model" {
				maxLen = 256
			}
			if !oneOf(key, "speed", "service_tier", "inference_geo", "model", "type") || len(typed) > maxLen {
				return false
			}
		case map[string]interface{}:
			if !validClaudeUsageObject(typed, depth+1) {
				return false
			}
		case []interface{}:
			if key != "iterations" || len(typed) > 1000 {
				return false
			}
			for _, item := range typed {
				entry, ok := item.(map[string]interface{})
				if !ok || !validClaudeUsageObject(entry, depth+1) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func validNumericUsageObject(obj map[string]interface{}, depth int) bool {
	if depth > 4 {
		return false
	}
	for _, value := range obj {
		switch typed := value.(type) {
		case json.Number, bool, nil:
			continue
		case map[string]interface{}:
			if !validNumericUsageObject(typed, depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func hasOnlyKeys(obj map[string]interface{}, allowed ...string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range obj {
		if _, ok := allowedSet[key]; !ok {
			return false
		}
	}
	return true
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func decodeObject(raw string) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	obj, ok := value.(map[string]interface{})
	if !ok || obj == nil {
		return nil, errors.New("usage evidence must be a JSON object")
	}
	return obj, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func marshalDeterministic(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func objectValue(value interface{}) map[string]interface{} {
	obj, _ := value.(map[string]interface{})
	return obj
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func boolValue(value interface{}) bool {
	result, _ := value.(bool)
	return result
}

func isBool(value interface{}) bool {
	_, ok := value.(bool)
	return ok
}

func explicitNumber(value interface{}) bool {
	switch value.(type) {
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}
