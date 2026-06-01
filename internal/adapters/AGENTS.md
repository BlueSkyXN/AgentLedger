# internal/adapters navigation card

`internal/adapters/` discovers and parses local agent logs into normalized `fingerprint.ParsedRecord` values. Read this card before modifying Claude, Codex, Copilot, Gemini parsing, model normalization, timing extraction, or token accounting. Key files: `adapter.go`, `<agent>.go`, `<agent>_test.go`, `codex_diagnostics.go`.

## Local invariants

- Do not guess undocumented log formats. Add synthetic fixtures or redacted samples that prove the parser behavior.
- Token counts come from explicit usage fields or documented adapter fallback only.
- Timing fields are set only when source logs provide explicit timing boundaries.
- Codex default accounting uses `total_token_usage` as a per-session cumulative counter and records deltas; `last_token_usage` is fallback or compatibility behavior.
- Cached input must be split into canonical `input_tokens` and `cache_read_tokens` when the source reports raw input including cache reads.
- `source_file`, `line_number`, raw hash, source product, observability level, and accounting method are diagnostics; preserve them when available.

## Local rules

- New adapters must implement discovery and parsing without mutating source logs.
- Parser errors should become warnings at import boundaries where possible, not whole-run panics.
- Keep model normalization centralized; do not duplicate provider/model mapping logic in each command or UI layer.

## Do not

- Do not commit real local agent logs, private session files, raw telemetry, or copied user paths as fixtures.
- Do not count synthetic zero-token model events as real usage unless the product decision changes.
- Do not sum source cumulative counters directly into report totals.

## Validation

- For adapter-only changes, start with `go test ./internal/adapters ./internal/fingerprint`.
- For import behavior changes, also run `go test ./cmd`.
- Finish risky parser/accounting work with root Go validation when the local toolchain allows it.
