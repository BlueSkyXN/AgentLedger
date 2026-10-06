package db

import "github.com/BlueSkyXN/AgentLedger/internal/model"

type semanticEventKey struct {
	sessionKey    string
	sessionID     string
	sessionPathID string
	turnID        string
	contentSHA256 string
}

func semanticKeyForEvent(event *model.UsageEvent) (semanticEventKey, bool) {
	// Equal per-request counters are not identity. Only Codex cumulative
	// observations without native request/message IDs qualify for this fallback.
	if event == nil || event.Channel != "codex" || event.SourceProduct != "codex-cli" ||
		event.IdentityVersion != model.IdentityVersion || event.IdentityStrategy != "session_record" ||
		event.IdentityScope != "session" || event.EventGranularity != "request" ||
		event.RequestID != "" || event.MessageID != "" || event.TotalTokens <= 0 ||
		event.TokenAccountingMethod != model.AccCodexTotalDelta ||
		event.SourceTotalTokens == nil || *event.SourceTotalTokens <= 0 || event.RawInputTokens == nil {
		return semanticEventKey{}, false
	}
	// Recompute from persisted facts rather than trusting a caller's hash. The
	// envelope includes optional counters, TTL, accounting and model evidence.
	hash, err := contentSHA256ForEvent(event)
	if err != nil {
		return semanticEventKey{}, false
	}
	return semanticEventKey{
		sessionKey:    event.SessionKey,
		sessionID:     event.SessionID,
		sessionPathID: event.SessionPathID,
		turnID:        event.TurnID,
		contentSHA256: hash,
	}, true
}

type semanticEventIndex map[semanticEventKey]int

func (index semanticEventIndex) add(event *model.UsageEvent) {
	if key, ok := semanticKeyForEvent(event); ok {
		index[key]++
	}
}

func (index semanticEventIndex) remove(event *model.UsageEvent) {
	if key, ok := semanticKeyForEvent(event); ok {
		if index[key] <= 1 {
			delete(index, key)
		} else {
			index[key]--
		}
	}
}

func (index semanticEventIndex) contains(event *model.UsageEvent) bool {
	key, ok := semanticKeyForEvent(event)
	return ok && index[key] > 0
}
