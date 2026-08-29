package model

import "testing"

func TestCanonicalModelIDStripsTrailingParenthetical(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "gpt-5.6-sol(max)", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol (max)", want: "gpt-5.6-sol"},
		{raw: "GPT-5.6-SOL (reasoning=xhigh)", want: "GPT-5.6-SOL"},
		{raw: "gpt-5.6-sol(effort=max)", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol(ultra)", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol(custom)", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol(max)(extra)", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol(note(max))", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol", want: "gpt-5.6-sol"},
		{raw: "gpt-5.6-sol(max", want: "gpt-5.6-sol(max"},
		{raw: "unknown", want: "unknown"},
	}
	for _, tt := range tests {
		if got := CanonicalModelID(tt.raw); got != tt.want {
			t.Fatalf("CanonicalModelID(%q)=%q, want %q", tt.raw, got, tt.want)
		}
	}
}
