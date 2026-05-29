package adapters

import "testing"

func TestAllAdaptersExcludesRemovedSources(t *testing.T) {
	adapters := AllAdapters()
	names := make(map[string]bool, len(adapters))
	for _, adapter := range adapters {
		names[adapter.Name()] = true
	}

	for _, name := range []string{"claude", "codex", "copilot", "gemini"} {
		if !names[name] {
			t.Fatalf("expected adapter %q to be registered", name)
		}
	}
	if names["qwen"] {
		t.Fatal("qwen adapter should not be registered")
	}
}
