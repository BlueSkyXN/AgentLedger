package model

import (
	"fmt"
	"math"
)

const (
	AntigravitySource  = "antigravity"
	AntigravityParser  = "antigravity-v2"
	AntigravityProfile = "antigravity_model_usage_v1"
)

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
}

func (o AntigravityOutput) Total() int64 { return o.Visible + o.Reasoning }

func (o AntigravityOutput) BetterThan(other AntigravityOutput) bool {
	reported, otherReported := o.Evidence >= AntigravityReportedOutput, other.Evidence >= AntigravityReportedOutput
	if reported != otherReported {
		return reported
	}
	if o.Total() != other.Total() {
		return o.Total() > other.Total()
	}
	if o.Evidence != other.Evidence {
		return o.Evidence > other.Evidence
	}
	return o.Reasoning > other.Reasoning
}

// AntigravityAccountingEvidence is covered by the accounting profile's content hash.
type AntigravityAccountingEvidence struct {
	Time   AntigravityTime
	Output AntigravityOutputEvidence
}

func (e AntigravityAccountingEvidence) Profile() string {
	return fmt.Sprintf("%s;time=%d;output=%d", AntigravityProfile, e.Time, e.Output)
}

func ParseAntigravityAccountingEvidence(profile string) (AntigravityAccountingEvidence, bool) {
	var evidence AntigravityAccountingEvidence
	n, err := fmt.Sscanf(profile, AntigravityProfile+";time=%d;output=%d", &evidence.Time, &evidence.Output)
	return evidence, err == nil && n == 2 && evidence.Profile() == profile &&
		evidence.Time >= AntigravityTrajectoryTime && evidence.Time <= AntigravityGenerationTime &&
		evidence.Output >= AntigravityDerivedOutput && evidence.Output <= AntigravityReportedOutputWithSplit
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
