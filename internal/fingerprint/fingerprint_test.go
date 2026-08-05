package fingerprint

import "testing"

func TestCanonicalJSONUsesNumberSortsKeysAndRejectsTrailingValues(t *testing.T) {
	one, err := CanonicalJSON(`{"z":2,"n":1}`)
	if err != nil || one != `{"n":1,"z":2}` {
		t.Fatalf("canonical integer=%q err=%v", one, err)
	}
	onePointZero, err := CanonicalJSON(`{"n":1.0,"z":2}`)
	if err != nil || onePointZero != `{"n":1.0,"z":2}` || one == onePointZero {
		t.Fatalf("numeric lexical form was lost: integer=%q decimal=%q err=%v", one, onePointZero, err)
	}
	for _, raw := range []string{`{"n":1} {"n":2}`, `{"n":`, `{"n":1} trailing`} {
		if _, err := CanonicalJSON(raw); err == nil {
			t.Fatalf("expected malformed/trailing JSON error for %q", raw)
		}
	}
}

func TestComputeIdentityPrecedenceAndRelocationStability(t *testing.T) {
	base := &ParsedRecord{
		Agent: "claude", SourceProduct: "claude-code", Provider: "anthropic", NativeSessionID: "session-1",
		NativeEventID: "event-1", IdentityKind: "event", IdentityScope: "session",
		IdentitySubkey: "segment-a", TimestampMs: 1700000000000, Model: "claude-sonnet",
		InputTokens: 10, OutputTokens: 4, TotalTokens: 14, TokenAccountingMethod: "sum",
		ProjectPath: "/private/a", SourceFile: "/private/a/session.jsonl", RawSHA256: "raw-a",
	}
	sessionA, eventA, strategyA, contentA, err := ComputeIdentity(base)
	if err != nil || strategyA != StrategyNativeEvent || sessionA == "" || eventA == "" || contentA == "" {
		t.Fatalf("unexpected identity: session=%q event=%q strategy=%q content=%q err=%v", sessionA, eventA, strategyA, contentA, err)
	}
	changed := *base
	changed.ProjectPath, changed.SourceFile, changed.RawSHA256, changed.ParserVersion = "/moved", "/moved/x", "other-raw", "claude-v2"
	sessionB, eventB, strategyB, contentB, err := ComputeIdentity(&changed)
	if err != nil || sessionA != sessionB || eventA != eventB || strategyA != strategyB || contentA != contentB {
		t.Fatalf("locator/parser metadata changed identity: %q/%q %q/%q %q/%q err=%v", sessionA, sessionB, eventA, eventB, contentA, contentB, err)
	}
}

func TestNativeIdentityIgnoresUsageButContentTracksIt(t *testing.T) {
	base := &ParsedRecord{
		Agent: "codex", SourceProduct: "codex-cli", Provider: "openai", NativeSessionID: "session",
		NativeEventID: "event", IdentityKind: "event", IdentityScope: "session", TimestampMs: 1,
		Model: "gpt-a", ModelNormalized: "gpt-a", ModelResolution: "direct_event", Granularity: "request",
		InputTokens: 1, TotalTokens: 1,
	}
	_, eventA, _, contentA, err := ComputeIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := *base
	changed.Model, changed.ModelNormalized = "gpt-b", "gpt-b"
	changed.InputTokens, changed.TotalTokens = 2, 2
	_, eventB, _, contentB, err := ComputeIdentity(&changed)
	if err != nil {
		t.Fatal(err)
	}
	if eventA != eventB || contentA == contentB {
		t.Fatalf("native identity/content contract failed event=%q/%q content=%q/%q", eventA, eventB, contentA, contentB)
	}
}

func TestComputeIdentityStrategies(t *testing.T) {
	base := ParsedRecord{Agent: "agent", SourceProduct: "product", NativeSessionID: "session", TimestampMs: 1, Granularity: "request"}
	tests := []struct {
		name     string
		mutate   func(*ParsedRecord)
		strategy Strategy
	}{
		{name: "native event", mutate: func(record *ParsedRecord) { record.NativeEventID, record.IdentityKind = "e", "event" }, strategy: StrategyNativeEvent},
		{name: "native message", mutate: func(record *ParsedRecord) { record.NativeEventID, record.IdentityKind = "m", "message" }, strategy: StrategyNativeMessage},
		{name: "native request", mutate: func(record *ParsedRecord) { record.NativeEventID, record.IdentityKind = "r", "request" }, strategy: StrategyNativeRequest},
		{name: "turn", mutate: func(record *ParsedRecord) { record.IdentityKind, record.TurnID = "turn", "t" }, strategy: StrategySessionTurn},
		{name: "record", mutate: func(record *ParsedRecord) { record.IdentityKind, record.IdentitySubkey = "record", "line-1" }, strategy: StrategySessionRecord},
		{name: "content fallback", mutate: func(record *ParsedRecord) {}, strategy: StrategyContentFallback},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.mutate(&record)
			_, eventID, strategy, _, err := ComputeIdentity(&record)
			if err != nil || eventID == "" || strategy != test.strategy {
				t.Fatalf("event=%q strategy=%q want=%q err=%v", eventID, strategy, test.strategy, err)
			}
		})
	}
}

func TestContentFallbackExcludesLocatorAndTracksFacts(t *testing.T) {
	base := &ParsedRecord{Agent: "gemini", SourceProduct: "gemini-cli", SessionPathID: "sessions/one", TimestampMs: 1700000000000, Model: "gemini-2", InputTokens: 10, OutputTokens: 2, TotalTokens: 12, TokenAccountingMethod: "usage"}
	_, first, strategy, contentA, err := ComputeIdentity(base)
	if err != nil || strategy != StrategyContentFallback || first == "" {
		t.Fatalf("expected content fallback: event=%q strategy=%q err=%v", first, strategy, err)
	}
	changed := *base
	changed.SourceFile, changed.RawSHA256, changed.ProjectPath = "/moved/source", "changed", "/moved/project"
	_, second, _, contentB, err := ComputeIdentity(&changed)
	if err != nil || first != second || contentA != contentB {
		t.Fatalf("locator leaked into fallback identity: %q/%q %q/%q err=%v", first, second, contentA, contentB, err)
	}
	changed.OutputTokens++
	_, third, _, contentC, err := ComputeIdentity(&changed)
	if err != nil || third == first || contentC == contentA {
		t.Fatalf("usage change did not change fallback identity: %q/%q %q/%q err=%v", first, third, contentA, contentC, err)
	}
}

func TestSourceProductSeparatesIdentities(t *testing.T) {
	base := ParsedRecord{Agent: "agent", SourceProduct: "product-a", NativeSessionID: "session", NativeEventID: "event", TimestampMs: 1}
	sessionA, eventA, _, _, err := ComputeIdentity(&base)
	if err != nil {
		t.Fatal(err)
	}
	base.SourceProduct = "product-b"
	sessionB, eventB, _, _, err := ComputeIdentity(&base)
	if err != nil {
		t.Fatal(err)
	}
	if sessionA == sessionB || eventA == eventB {
		t.Fatal("different source products must not deduplicate")
	}
}

func TestIdentityTuplePreservesNativeIDAndSubkeyBoundaries(t *testing.T) {
	base := ParsedRecord{
		Agent: "agent", SourceProduct: "product", NativeSessionID: "session",
		NativeEventID: "a|b", IdentityKind: "event", IdentitySubkey: "c",
		TimestampMs: 1, Granularity: "request",
	}
	_, eventA, _, _, err := ComputeIdentity(&base)
	if err != nil {
		t.Fatal(err)
	}
	base.NativeEventID = "a"
	base.IdentitySubkey = "b|c"
	_, eventB, _, _, err := ComputeIdentity(&base)
	if err != nil {
		t.Fatal(err)
	}
	if eventA == eventB {
		t.Fatal("identity tuple boundaries collapsed before hashing")
	}
}

func TestComputeIdentityRequiresTimestampAndStableSession(t *testing.T) {
	noTimestamp := &ParsedRecord{Agent: "codex", SourceProduct: "codex-cli", NativeSessionID: "s", NativeEventID: "e"}
	if _, _, _, _, err := ComputeIdentity(noTimestamp); err == nil {
		t.Fatal("expected missing timestamp error")
	}
	noSession := &ParsedRecord{Agent: "codex", SourceProduct: "codex-cli", NativeEventID: "e", TimestampMs: 1}
	if _, _, _, _, err := ComputeIdentity(noSession); err == nil {
		t.Fatal("expected missing stable session error")
	}
	absPath := &ParsedRecord{Agent: "codex", SourceProduct: "codex-cli", SessionPathID: "/private/session.jsonl", NativeEventID: "e", TimestampMs: 1}
	if _, _, _, _, err := ComputeIdentity(absPath); err == nil {
		t.Fatal("expected absolute session path rejection")
	}
}
