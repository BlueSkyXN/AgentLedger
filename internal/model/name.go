package model

import "strings"

// CanonicalModelID trims a source model string and drops trailing parenthetical
// suffixes such as "(max)" or "(reasoning=xhigh)". Thinking or effort labels in
// those suffixes do not become part of the stored model ID. The remaining ID
// keeps its original casing.
func CanonicalModelID(raw string) string {
	raw = strings.TrimSpace(raw)
	for {
		base, ok := StripTrailingParenthetical(raw)
		if !ok {
			return raw
		}
		raw = base
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
