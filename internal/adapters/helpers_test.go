package adapters

import "testing"

func TestNormalizeModelNameStripsReasoningSuffix(t *testing.T) {
	normalized, provider, family := NormalizeModelName("gpt-5.6-sol(max)")
	if normalized != "gpt-5.6-sol" || provider != "openai" || family != "gpt" {
		t.Fatalf("got normalized=%q provider=%q family=%q", normalized, provider, family)
	}
}
