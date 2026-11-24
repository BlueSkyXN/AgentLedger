package model

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	AntigravitySource            = "antigravity"
	AntigravityParser            = "antigravity-v2"
	AntigravityProfile           = "antigravity_model_usage_v1"
	AntigravityDefaultModel      = "gemini-internal-model"
	AntigravityPlaceholderPrefix = "model_placeholder_m"
	AntigravityUnresolvedPrefix  = "antigravity-model-"
)

func IsAntigravityModelPlaceholder(name string) bool {
	return strings.HasPrefix(name, AntigravityPlaceholderPrefix) || strings.HasPrefix(name, AntigravityUnresolvedPrefix)
}

// AntigravityModelPriority places conflicting context between an inferred
// name (rank 1) and direct model evidence (rank 2 or higher).
func AntigravityModelPriority(rank int, conflict bool) int {
	if conflict {
		return 3
	}
	return rank * 2
}

type AntigravityTime uint8

const (
	AntigravityTrajectoryTime AntigravityTime = iota + 1
	AntigravityStepTime
	AntigravityGenerationTime
)

type AntigravityOutputEvidence uint8

const (
	AntigravityDerivedOutput AntigravityOutputEvidence = iota + 1
	AntigravityReportedOutput
	AntigravityReportedOutputWithPart
	AntigravityReportedOutputWithSplit
)

// AntigravityOutput keeps a complete split from one observation. The visible
// and reasoning buckets always partition that observation's output total.
type AntigravityOutput struct {
	Visible, Reasoning int64
	Evidence           AntigravityOutputEvidence
	DiscardedTotal     *int64
}

func (o AntigravityOutput) Total() int64 { return o.Visible + o.Reasoning }

func (o AntigravityOutput) BetterThan(other AntigravityOutput) bool {
	evidence, otherEvidence := o.effectiveEvidence(), other.effectiveEvidence()
	reported, otherReported := evidence >= AntigravityReportedOutput, otherEvidence >= AntigravityReportedOutput
	if reported != otherReported {
		return reported
	}
	if o.Total() != other.Total() {
		return o.Total() > other.Total()
	}
	if evidence != otherEvidence {
		return evidence > otherEvidence
	}
	if o.Reasoning != other.Reasoning {
		return o.Reasoning > other.Reasoning
	}
	return o.DiscardedTotal != nil && (other.DiscardedTotal == nil || *o.DiscardedTotal > *other.DiscardedTotal)
}

func (o AntigravityOutput) effectiveEvidence() AntigravityOutputEvidence {
	// The original parser assigned reported evidence to an encoded zero and
	// full-split evidence to a visible-only observation.
	switch {
	case o.Total() == 0:
		return AntigravityDerivedOutput
	case o.Evidence == AntigravityReportedOutputWithSplit && (o.Visible == 0 || o.Reasoning == 0):
		return AntigravityReportedOutputWithPart
	default:
		return o.Evidence
	}
}

// RepairsLegacy checks the exact output projection an older parser made
// from these same observed parts and the conflicting reported total.
func (o AntigravityOutput) RepairsLegacy(legacy AntigravityOutput) bool {
	if o.DiscardedTotal == nil || legacy.DiscardedTotal != nil || *o.DiscardedTotal != legacy.Total() {
		return false
	}
	total := *o.DiscardedTotal
	evidence := AntigravityReportedOutput
	var reasoning int64
	switch {
	case o.Reasoning > 0 && o.Reasoning <= total:
		evidence, reasoning = AntigravityReportedOutputWithPart, o.Reasoning
	case o.Visible > 0 && o.Visible <= total:
		evidence, reasoning = AntigravityReportedOutputWithPart, total-o.Visible
	}
	return legacy.Evidence == evidence && legacy.Reasoning == reasoning
}

// AntigravityAccountingEvidence is covered by the accounting profile's content hash.
type AntigravityAccountingEvidence struct {
	Time                 AntigravityTime
	Output               AntigravityOutputEvidence
	DiscardedOutputTotal *int64
	ModelConflict        bool
}

func (e AntigravityAccountingEvidence) Profile() string {
	profile := fmt.Sprintf("%s;time=%d;output=%d", AntigravityProfile, e.Time, e.Output)
	if e.DiscardedOutputTotal != nil {
		profile += ";discarded_output_total=" + strconv.FormatInt(*e.DiscardedOutputTotal, 10)
	}
	if e.ModelConflict {
		profile += ";model_conflict=1"
	}
	return profile
}

func ParseAntigravityAccountingEvidence(profile string) (AntigravityAccountingEvidence, bool) {
	var evidence AntigravityAccountingEvidence
	base, conflict := strings.CutSuffix(profile, ";model_conflict=1")
	evidence.ModelConflict = conflict
	base, discardedTotal, hasDiscardedTotal := strings.Cut(base, ";discarded_output_total=")
	if hasDiscardedTotal {
		value, err := strconv.ParseInt(discardedTotal, 10, 64)
		if err != nil || value < 0 {
			return AntigravityAccountingEvidence{}, false
		}
		evidence.DiscardedOutputTotal = &value
	}
	n, err := fmt.Sscanf(base, AntigravityProfile+";time=%d;output=%d", &evidence.Time, &evidence.Output)
	return evidence, err == nil && n == 2 && evidence.Profile() == profile &&
		evidence.Time >= AntigravityTrajectoryTime && evidence.Time <= AntigravityGenerationTime &&
		evidence.Output >= AntigravityDerivedOutput && evidence.Output <= AntigravityReportedOutputWithSplit &&
		(!hasDiscardedTotal || evidence.Output == AntigravityDerivedOutput)
}

// AntigravityRequestIdentity is encoded in request_id. It contains only event
// hashes from native IDs, session-scoped row keys and compatible v1 identities.
type AntigravityRequestIdentity struct {
	Native []string `json:"native,omitempty"`
	Rows   []string `json:"rows,omitempty"`
	Legacy []string `json:"legacy,omitempty"`
}

func AntigravityTotal(buckets ...int64) (int64, bool) {
	var total int64
	for _, bucket := range buckets {
		if bucket < 0 || bucket > math.MaxInt64-total {
			return 0, false
		}
		total += bucket
	}
	return total, true
}
