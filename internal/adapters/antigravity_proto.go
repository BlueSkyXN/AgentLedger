package adapters

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Antigravity stores per-request usage inside SQLite conversation databases as
// protobuf blobs without a published schema. The decoders below follow the
// wire-level layout that the Antigravity CLI writes today (chat model metadata
// in gen_metadata, step metadata in steps); the field numbers are a documented
// reverse-engineering contract, not a guess. Only structured usage facts are
// extracted; context payloads that may embed private paths are never decoded.

const (
	protoWireVarint  = 0
	protoWireFixed64 = 1
	protoWireBytes   = 2
	protoWireFixed32 = 5
)

type protoField struct {
	number int
	kind   byte
	value  uint64
	bytes  []byte
}

// decodeProtoFields decodes one protobuf message into its top-level fields.
// Unknown fields keep their wire type so callers can ignore them safely.
func decodeProtoFields(blob []byte) ([]protoField, error) {
	var fields []protoField
	for len(blob) > 0 {
		tag, rest, err := readProtoVarint(blob)
		if err != nil {
			return nil, err
		}
		number := int(tag >> 3)
		if number <= 0 || number > math.MaxInt32 {
			return nil, fmt.Errorf("invalid protobuf field number %d", number)
		}
		wireType := byte(tag & 7)
		field := protoField{number: number, kind: wireType}
		switch wireType {
		case protoWireVarint:
			value, valueRest, err := readProtoVarint(rest)
			if err != nil {
				return nil, err
			}
			field.value = value
			rest = valueRest
		case protoWireFixed64:
			if len(rest) < 8 {
				return nil, fmt.Errorf("truncated protobuf fixed64 value")
			}
			rest = rest[8:]
		case protoWireBytes:
			length, lengthRest, err := readProtoVarint(rest)
			if err != nil {
				return nil, err
			}
			if length > uint64(len(lengthRest)) {
				return nil, fmt.Errorf("truncated protobuf length-delimited value")
			}
			field.bytes = lengthRest[:length]
			rest = lengthRest[length:]
		case protoWireFixed32:
			if len(rest) < 4 {
				return nil, fmt.Errorf("truncated protobuf fixed32 value")
			}
			rest = rest[4:]
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", wireType)
		}
		fields = append(fields, field)
		blob = rest
	}
	return fields, nil
}

func readProtoVarint(blob []byte) (uint64, []byte, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if len(blob) == 0 {
			return 0, nil, fmt.Errorf("truncated protobuf varint")
		}
		byteValue := blob[0]
		blob = blob[1:]
		payload := uint64(byteValue & 0x7f)
		if shift == 63 && payload > 1 {
			return 0, nil, fmt.Errorf("protobuf varint overflow")
		}
		value |= payload << shift
		if byteValue&0x80 == 0 {
			return value, blob, nil
		}
	}
	return 0, nil, fmt.Errorf("protobuf varint overflow")
}

// protoVarint returns the last varint occurrence of a field, matching the
// protobuf "last oneof member wins" behavior of generated decoders.
func protoVarint(fields []protoField, number int) (uint64, bool) {
	for i := len(fields) - 1; i >= 0; i-- {
		if fields[i].number == number && fields[i].kind == protoWireVarint {
			return fields[i].value, true
		}
	}
	return 0, false
}

// protoBytes returns the first length-delimited occurrence of a field.
func protoBytes(fields []protoField, number int) ([]byte, bool) {
	for _, field := range fields {
		if field.number == number && field.kind == protoWireBytes {
			return field.bytes, true
		}
	}
	return nil, false
}

func protoBytesAll(fields []protoField, number int) [][]byte {
	var values [][]byte
	for _, field := range fields {
		if field.number == number && field.kind == protoWireBytes {
			values = append(values, field.bytes)
		}
	}
	return values
}

// protoText returns the last non-blank UTF-8 text occurrence of a field.
func protoText(fields []protoField, number int) string {
	for i := len(fields) - 1; i >= 0; i-- {
		field := fields[i]
		if field.number != number || field.kind != protoWireBytes {
			continue
		}
		text := strings.TrimSpace(string(field.bytes))
		if text != "" {
			return text
		}
	}
	return ""
}

// antigravityModelUsage is the per-request usage message embedded in both
// generator and step metadata.
type antigravityModelUsage struct {
	modelID             uint64
	hasModelID          bool
	inputTokens         uint64
	totalOutputTokens   uint64
	cacheCreationTokens uint64
	cacheReadTokens     uint64
	reasoningTokens     uint64
	visibleOutputTokens uint64
	provider            uint64
	hasProvider         bool
	messageID           string
	responseID          string
	providerAssignedID  string
}

func parseAntigravityModelUsage(blob []byte) (antigravityModelUsage, error) {
	fields, err := decodeProtoFields(blob)
	if err != nil {
		return antigravityModelUsage{}, err
	}
	usage := antigravityModelUsage{
		messageID:          protoText(fields, 7),
		responseID:         protoText(fields, 11),
		providerAssignedID: protoText(fields, 12),
	}
	if value, ok := protoVarint(fields, 1); ok && value != 0 {
		usage.modelID, usage.hasModelID = value, true
	}
	usage.inputTokens, _ = protoVarint(fields, 2)
	usage.totalOutputTokens, _ = protoVarint(fields, 3)
	usage.cacheCreationTokens, _ = protoVarint(fields, 4)
	usage.cacheReadTokens, _ = protoVarint(fields, 5)
	if value, ok := protoVarint(fields, 6); ok && value != 0 {
		usage.provider, usage.hasProvider = value, true
	}
	usage.reasoningTokens, _ = protoVarint(fields, 9)
	usage.visibleOutputTokens, _ = protoVarint(fields, 10)
	return usage, nil
}

func (u antigravityModelUsage) isTokenBearing() bool {
	return u.inputTokens > 0 || u.totalOutputTokens > 0 || u.cacheCreationTokens > 0 ||
		u.cacheReadTokens > 0 || u.reasoningTokens > 0 || u.visibleOutputTokens > 0
}

func (u antigravityModelUsage) sameTokenCounts(other antigravityModelUsage) bool {
	return u.modelID == other.modelID &&
		u.inputTokens == other.inputTokens &&
		u.totalOutputTokens == other.totalOutputTokens &&
		u.cacheCreationTokens == other.cacheCreationTokens &&
		u.cacheReadTokens == other.cacheReadTokens &&
		u.reasoningTokens == other.reasoningTokens &&
		u.visibleOutputTokens == other.visibleOutputTokens
}

// identityKeys lists the provider-assigned identifiers of this usage in
// preference order fragments. The prefix keeps the three id kinds disjoint.
func (u antigravityModelUsage) identityKeys() []string {
	var keys []string
	for _, key := range []struct{ prefix, value string }{
		{"response", u.responseID},
		{"provider", u.providerAssignedID},
		{"message", u.messageID},
	} {
		if strings.TrimSpace(key.value) != "" {
			keys = append(keys, key.prefix+":"+key.value)
		}
	}
	return keys
}

// preferredMessageID picks the strongest native identifier: response id over
// provider-assigned id over message id. The rank records which kind won.
func (u antigravityModelUsage) preferredMessageID() (string, int) {
	for _, candidate := range []struct {
		value string
		rank  int
	}{
		{u.responseID, 3},
		{u.providerAssignedID, 2},
		{u.messageID, 1},
	} {
		if strings.TrimSpace(candidate.value) != "" {
			return candidate.value, candidate.rank
		}
	}
	return "", 0
}

// isMirrorOf reports whether this usage repeats the primary attempt. Retry
// lists may repeat the primary usage; dropping one mirror keeps genuinely
// distinct retries.
func (u antigravityModelUsage) isMirrorOf(primary antigravityModelUsage) bool {
	if !u.sameTokenCounts(primary) {
		return false
	}
	primaryKeys := primary.identityKeys()
	for _, key := range u.identityKeys() {
		found := false
		for _, candidate := range primaryKeys {
			if candidate == key {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// dropAntigravityPrimaryMirror removes a single retry that mirrors the primary
// usage, preserving the usage of distinct retries.
func dropAntigravityPrimaryMirror(primary *antigravityModelUsage, retries []antigravityModelUsage) []antigravityModelUsage {
	if primary == nil {
		return retries
	}
	for index, retry := range retries {
		if retry.isMirrorOf(*primary) {
			return append(retries[:index:index], retries[index+1:]...)
		}
	}
	return retries
}

// antigravityGeneratorMetadata is the gen_metadata blob: root field 1 is the
// chat model message, root field 2 carries linked step indices.
type antigravityGeneratorMetadata struct {
	model       string
	modelID     uint64
	hasModelID  bool
	usage       *antigravityModelUsage
	retryUsages []antigravityModelUsage
	timestampMs int64
	stepIndices []int64
}

func parseAntigravityGeneratorMetadata(blob []byte) (antigravityGeneratorMetadata, error) {
	root, err := decodeProtoFields(blob)
	if err != nil {
		return antigravityGeneratorMetadata{}, err
	}
	chatModel, ok := protoBytes(root, 1)
	if !ok {
		return antigravityGeneratorMetadata{}, fmt.Errorf("missing chat model field 1")
	}
	chatFields, err := decodeProtoFields(chatModel)
	if err != nil {
		return antigravityGeneratorMetadata{}, err
	}
	metadata := antigravityGeneratorMetadata{
		model: firstNonEmpty(protoText(chatFields, 19), protoText(chatFields, 21)),
	}
	if value, ok := protoVarint(chatFields, 3); ok && value != 0 {
		metadata.modelID, metadata.hasModelID = value, true
	}
	if usageBlob, ok := protoBytes(chatFields, 4); ok {
		usage, err := parseAntigravityModelUsage(usageBlob)
		if err != nil {
			return antigravityGeneratorMetadata{}, err
		}
		metadata.usage = &usage
	}
	retries, err := parseAntigravityRetryInfos(chatFields, 17)
	if err != nil {
		return antigravityGeneratorMetadata{}, err
	}
	metadata.retryUsages = dropAntigravityPrimaryMirror(metadata.usage, retries)
	if infoBlob, ok := protoBytes(chatFields, 9); ok {
		infoFields, err := decodeProtoFields(infoBlob)
		if err != nil {
			return antigravityGeneratorMetadata{}, err
		}
		if timestampBlob, ok := protoBytes(infoFields, 4); ok {
			if timestamp, ok := parseAntigravityTimestamp(timestampBlob); ok {
				metadata.timestampMs = timestamp
			}
		}
	}
	metadata.stepIndices = parseAntigravityStepIndices(root)
	return metadata, nil
}

func parseAntigravityRetryInfos(fields []protoField, number int) ([]antigravityModelUsage, error) {
	blobs := protoBytesAll(fields, number)
	usages := make([]antigravityModelUsage, 0, len(blobs))
	for _, blob := range blobs {
		retryFields, err := decodeProtoFields(blob)
		if err != nil {
			return nil, err
		}
		usageBlob, ok := protoBytes(retryFields, 2)
		if !ok {
			continue
		}
		usage, err := parseAntigravityModelUsage(usageBlob)
		if err != nil {
			return nil, err
		}
		usages = append(usages, usage)
	}
	return usages, nil
}

func parseAntigravityStepIndices(root []protoField) []int64 {
	var indices []int64
	for _, field := range root {
		if field.number != 2 {
			continue
		}
		switch field.kind {
		case protoWireVarint:
			if field.value <= math.MaxInt64 {
				indices = append(indices, int64(field.value))
			}
		case protoWireBytes:
			packed := field.bytes
			for len(packed) > 0 {
				value, rest, err := readProtoVarint(packed)
				if err != nil || value > math.MaxInt64 {
					break
				}
				indices = append(indices, int64(value))
				packed = rest
			}
		}
	}
	return indices
}

// antigravityStepMetadata is the steps.metadata blob.
type antigravityStepMetadata struct {
	model       string
	modelID     uint64
	hasModelID  bool
	provider    uint64
	hasProvider bool
	usage       *antigravityModelUsage
	retryUsages []antigravityModelUsage
	timestampMs int64
}

func parseAntigravityStepMetadata(blob []byte) (antigravityStepMetadata, error) {
	fields, err := decodeProtoFields(blob)
	if err != nil {
		return antigravityStepMetadata{}, err
	}
	metadata := antigravityStepMetadata{}
	if infoBlob, ok := protoBytes(fields, 24); ok {
		infoFields, err := decodeProtoFields(infoBlob)
		if err != nil {
			return antigravityStepMetadata{}, err
		}
		metadata.model = firstNonEmpty(protoText(infoFields, 12), protoText(infoFields, 8))
		if value, ok := protoVarint(infoFields, 1); ok && value != 0 {
			metadata.modelID, metadata.hasModelID = value, true
		}
		if value, ok := protoVarint(infoFields, 7); ok && value != 0 {
			metadata.provider, metadata.hasProvider = value, true
		}
	}
	if usageBlob, ok := protoBytes(fields, 9); ok {
		usage, err := parseAntigravityModelUsage(usageBlob)
		if err != nil {
			return antigravityStepMetadata{}, err
		}
		metadata.usage = &usage
	}
	retries, err := parseAntigravityRetryInfos(fields, 28)
	if err != nil {
		return antigravityStepMetadata{}, err
	}
	metadata.retryUsages = dropAntigravityPrimaryMirror(metadata.usage, retries)
	for _, number := range []int{8, 1} {
		if timestampBlob, ok := protoBytes(fields, number); ok {
			if timestamp, ok := parseAntigravityTimestamp(timestampBlob); ok {
				metadata.timestampMs = timestamp
				break
			}
		}
	}
	return metadata, nil
}

// parseAntigravityTimestamp converts a google.protobuf.Timestamp message to
// milliseconds, saturating instead of overflowing.
func parseAntigravityTimestamp(blob []byte) (int64, bool) {
	fields, err := decodeProtoFields(blob)
	if err != nil {
		return 0, false
	}
	seconds, ok := protoVarint(fields, 1)
	if !ok || seconds == 0 || seconds > math.MaxInt64 {
		return 0, false
	}
	nanos, _ := protoVarint(fields, 2)
	if nanos > 999_999_999 {
		nanos = 999_999_999
	}
	millis := seconds * 1000
	if millis/1000 != seconds {
		return math.MaxInt64, true
	}
	millis += nanos / 1_000_000
	if millis > math.MaxInt64 {
		return math.MaxInt64, true
	}
	return int64(millis), true
}

// parseAntigravityTrajectoryTimestamp reads the trajectory metadata blob; its
// field 2 is a Timestamp used as the session-level fallback.
func parseAntigravityTrajectoryTimestamp(blob []byte) (int64, bool) {
	fields, err := decodeProtoFields(blob)
	if err != nil {
		return 0, false
	}
	timestampBlob, ok := protoBytes(fields, 2)
	if !ok {
		return 0, false
	}
	return parseAntigravityTimestamp(timestampBlob)
}

// antigravityDefaultModelName marks events where neither the usage nor the row
// carried any usable model evidence.
const antigravityDefaultModelName = "gemini-internal-model"

const (
	antigravityPlaceholderPrefix  = "model_placeholder_m"
	antigravityUnresolvedIDPrefix = "antigravity-model-"
)

// antigravityModelResolution ranks how strong a model name evidence is; higher
// wins when mirror events disagree.
const (
	antigravityModelDefault = iota
	antigravityModelPlaceholder
	antigravityModelResolved
)

// antigravityModelNameFromID maps Antigravity's internal model enum to public
// model names. Ids at or above 1000 are placeholder ids (id minus 1000).
func antigravityModelNameFromID(id uint64) string {
	switch id {
	case 246:
		return "gemini-2.5-pro"
	case 312:
		return "gemini-2.5-flash"
	case 313, 329:
		return "gemini-2.5-flash-thinking"
	case 330:
		return "gemini-2.5-flash-lite"
	case 281, 282:
		return "claude-sonnet-4"
	case 290, 291:
		return "claude-opus-4"
	case 333, 334:
		return "claude-4.5-sonnet"
	case 340, 341:
		return "claude-4.5-haiku"
	case 342:
		return "model_openai_gpt_oss_120b_medium"
	}
	if id >= 1000 {
		return fmt.Sprintf("%s%d", antigravityPlaceholderPrefix, id-1000)
	}
	return fmt.Sprintf("%s%d", antigravityUnresolvedIDPrefix, id)
}

func antigravityModelFromID(id uint64) (string, bool) {
	if id == 0 {
		return "", false
	}
	name, err := normalizeAntigravityModel(antigravityModelNameFromID(id))
	if err != nil {
		return "", false
	}
	return name, true
}

// antigravityRowModel resolves the model evidence attached to a metadata row:
// the display name wins, the model id is the fallback.
func antigravityRowModel(model string, modelID uint64, hasModelID bool) string {
	if strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model)
	}
	if !hasModelID {
		return ""
	}
	if name, ok := antigravityModelFromID(modelID); ok {
		return name
	}
	return ""
}

func antigravityModelResolution(model string) int {
	switch {
	case model == antigravityDefaultModelName:
		return antigravityModelDefault
	case strings.HasPrefix(model, antigravityPlaceholderPrefix),
		strings.HasPrefix(model, antigravityUnresolvedIDPrefix):
		return antigravityModelPlaceholder
	default:
		return antigravityModelResolved
	}
}

func isResolvedAntigravityModel(model string) bool {
	return antigravityModelResolution(model) == antigravityModelResolved
}

var errAntigravityModelNormalization = fmt.Errorf("empty antigravity model name")

// normalizeAntigravityModel canonicalizes the display names Antigravity logs
// (for example "Gemini 3 Pro (high)") into public model ids. Unknown names keep
// their source spelling with spaces converted for known vendor prefixes.
func normalizeAntigravityModel(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errAntigravityModelNormalization
	}
	lower := strings.ToLower(trimmed)
	if variant, ok := antigravityFlashEffortVariant(lower); ok {
		return variant, nil
	}
	base := lower
	if index := strings.Index(base, "("); index >= 0 {
		base = strings.TrimSpace(base[:index])
	}
	if normalized, ok := antigravityModelAliases[base]; ok {
		return normalized, nil
	}
	converted := strings.ReplaceAll(base, " ", "-")
	if strings.HasPrefix(converted, "gemini-") || strings.HasPrefix(converted, "claude-") || strings.HasPrefix(converted, "gpt-") {
		return converted, nil
	}
	return trimmed, nil
}

// antigravityModelAliases maps known lowercase display names and placeholder
// ids to public model names.
var antigravityModelAliases = map[string]string{
	"gemini 3.8 flash":                 "gemini-3.8-flash",
	"gemini 3.8 flash thinking":        "gemini-3.8-flash",
	"gemini 3.7 flash":                 "gemini-3.7-flash",
	"gemini 3.7 flash thinking":        "gemini-3.7-flash",
	"gemini 3.7 pro":                   "gemini-3.7-pro",
	"gemini 3.7 pro thinking":          "gemini-3.7-pro",
	"gemini 3.6 flash":                 "gemini-3.6-flash",
	"gemini 3 flash":                   "gemini-3.6-flash",
	"gemini 3.6 pro":                   "gemini-3.6-pro",
	"gemini 3 pro":                     "gemini-3-pro",
	"gemini 3 pro thinking":            "gemini-3-pro",
	"gemini 2.5 flash":                 "gemini-2.5-flash",
	"gemini 2.5 pro":                   "gemini-2.5-pro",
	"gemini 2.0 flash":                 "gemini-2.0-flash",
	"gemini 2 flash":                   "gemini-2.0-flash",
	"gemini 2.0 pro":                   "gemini-2.0-pro",
	"gemini 1.5 flash":                 "gemini-1.5-flash",
	"gemini 1.5 pro":                   "gemini-1.5-pro",
	"model_placeholder_m318":           "gemini-3.8-flash-high",
	"model_placeholder_m319":           "gemini-3.8-flash-medium",
	"model_placeholder_m320":           "gemini-3.8-flash-low",
	"model_placeholder_m298":           "gemini-3.7-flash-high",
	"model_placeholder_m299":           "gemini-3.7-flash-medium",
	"model_placeholder_m300":           "gemini-3.7-flash-low",
	"model_placeholder_m71":            "gemini-3.6-flash-high",
	"model_placeholder_m72":            "gemini-3.6-flash-medium",
	"model_placeholder_m73":            "gemini-3.6-flash-low",
	"model_placeholder_m417":           "gemini-4-argon",
	"model_placeholder_m418":           "gemini-4-argon",
	"model_placeholder_m419":           "gemini-4-argon",
	"model_placeholder_m402":           "claude-opus-5-5",
	"model_placeholder_m50":            "gemini-3.1-flash-lite",
	"model_placeholder_m26":            "claude-opus-4-6",
	"model_placeholder_m35":            "claude-sonnet-4-6",
	"model_placeholder_m36":            "gemini-3.1-pro",
	"model_placeholder_m37":            "gemini-3.1-pro",
	"model_placeholder_m16":            "gemini-3.1-pro",
	"model_placeholder_m18":            "gemini-3-flash-preview",
	"model_placeholder_m84":            "gemini-3-flash-preview",
	"model_placeholder_m47":            "gemini-3-flash-preview",
	"model_placeholder_m132":           "gemini-3.5-flash-high",
	"model_placeholder_m133":           "gemini-3.5-flash-high",
	"model_placeholder_m187":           "gemini-3.5-flash-extra-low",
	"model_placeholder_m20":            "gemini-3.5-flash-medium",
	"model_openai_gpt_oss_120b_medium": "gpt-oss-120b-medium",
	"gemini-pro-default":               "gemini-3.1-pro",
	"gemini-pro-agent":                 "gemini-3.1-pro",
	"gemini-3-flash-agent":             "gemini-3.5-flash-high",
	"gemini-3-flash-agent-a":           "gemini-3.5-flash-high",
	"gemini-3-flash-agent-b":           "gemini-3.5-flash-high",
	"gemini-3-flash-a":                 "gemini-3.5-flash-high",
	"gemini-3-flash-b":                 "gemini-3.5-flash-high",
	"gemini-3-flash-c":                 "gemini-3-flash-preview",
	"gemini-3-flash":                   "gemini-3-flash-preview",
	"gemini-3.5-flash-low":             "gemini-3.5-flash-medium",
	"gemini-3.1-pro-high":              "gemini-3.1-pro",
	"gemini-3.1-pro-low":               "gemini-3.1-pro",
	"gemini-3-pro-high":                "gemini-3-pro",
	"gemini-3-pro-low":                 "gemini-3-pro",
	"claude 3.7 sonnet":                "claude-3-7-sonnet",
	"claude 3.7 sonnet thinking":       "claude-3-7-sonnet",
	"claude 3.5 sonnet":                "claude-3-5-sonnet",
	"claude 3.5 haiku":                 "claude-3-5-haiku",
	"claude 3 opus":                    "claude-3-opus",
}

var antigravityFlashEffortFamilies = map[string]bool{
	"gemini-3.8-flash": true,
	"gemini-3.7-flash": true,
	"gemini-3.6-flash": true,
}

var antigravityFlashEfforts = map[string]bool{
	"high":   true,
	"medium": true,
	"low":    true,
}

// antigravityFlashEffortVariant recognizes display names like
// "gemini 3.6 flash (high)" and returns the priced family variant.
func antigravityFlashEffortVariant(lower string) (string, bool) {
	base, rest, found := strings.Cut(lower, " (")
	if !found || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	effort := strings.TrimSuffix(rest, ")")
	if !antigravityFlashEfforts[effort] || strings.Contains(base, "-") {
		return "", false
	}
	variant := strings.ReplaceAll(base, " ", "-") + "-" + effort
	if antigravityFlashEffortFamilies[strings.TrimSuffix(variant, "-"+effort)] {
		return variant, true
	}
	return "", false
}

// sortAntigravityIdentities returns the identity keys in a stable order so
// merged events always pick the same fallback label.
func sortAntigravityIdentities(keys []string) []string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	return sorted
}
