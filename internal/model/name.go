package model

import "strings"

// CanonicalModelID trims a source model string and drops recognized trailing
// annotations. Parenthetical suffixes such as "(max)" or "(reasoning=xhigh)"
// and the explicit one-million-context marker "[1m]" do not become part of the
// stored model ID. The remaining ID keeps its original casing.
func CanonicalModelID(raw string) string {
	raw = strings.TrimSpace(raw)
	for {
		if base, ok := StripTrailingParenthetical(raw); ok {
			raw = base
			continue
		}
		if base, ok := StripTrailingOneMillionContext(raw); ok {
			raw = base
			continue
		}
		return raw
	}
}

// StripTrailingParenthetical removes one complete trailing "(...)" group,
// including optional whitespace before the opening parenthesis.
func StripTrailingParenthetical(model string) (string, bool) {
	model = strings.TrimRight(model, " 	")
	if !strings.HasSuffix(model, ")") {
		return model, false
	}
	depth := 0
	for index := len(model) - 1; index >= 0; index-- {
		switch model[index] {
		case ')':
			depth++
		case '(':
			depth--
			if depth != 0 {
				continue
			}
			if index == 0 {
				return model, false
			}
			base := strings.TrimSpace(model[:index])
			if base == "" {
				return model, false
			}
			return base, true
		}
	}
	return model, false
}

// StripTrailingOneMillionContext removes one case-insensitive trailing "[1m]"
// marker. Other bracketed suffixes remain part of the model ID.
func StripTrailingOneMillionContext(model string) (string, bool) {
	model = strings.TrimRight(model, " \t")
	const suffix = "[1m]"
	if len(model) <= len(suffix) || !strings.EqualFold(model[len(model)-len(suffix):], suffix) {
		return model, false
	}
	base := strings.TrimSpace(model[:len(model)-len(suffix)])
	if base == "" {
		return model, false
	}
	return base, true
}
