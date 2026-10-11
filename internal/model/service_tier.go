package model

import "strings"

func NormalizeServiceTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast", "priority":
		return "priority"
	case "default", "standard":
		return "default"
	case "flex", "batch":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func AccountingProfileWithTier(profile, tier string) string {
	base, _, _ := strings.Cut(profile, ";service_tier=")
	if tier = NormalizeServiceTier(tier); tier != "" {
		return base + ";service_tier=" + tier
	}
	return base
}

func ServiceTierFromAccountingProfile(channel, profile string) string {
	if channel != "codex" {
		return ""
	}
	base, value, found := strings.Cut(profile, ";service_tier=")
	if !found || base != "ledger" && base != "ccusage_compatible" {
		return ""
	}
	return NormalizeServiceTier(value)
}
