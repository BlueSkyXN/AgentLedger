# cmd navigation card

`cmd/` is the Cobra CLI command surface for AgentLedger. Read this card before modifying command registration, flags, help text, stdout/stderr, or flows that read/write the configured SQLite database. Key files: `root.go`, `import.go`, `export.go`, `merge.go`, `report.go`, `serve.go`.

## Local invariants

- `root.go` must register every public command exactly once.
- Commands should return errors from `RunE` instead of calling `os.Exit`, except the root `Execute()` boundary.
- `import` must always attempt to finish its `import_runs` row, including warning summaries.
- `report` flags and API filters should stay aligned: `since`, `until`, `channel`, `provider`, `model`, `session`.
- `serve` must validate loopback addresses before opening the database or binding a listener.

## Local rules

- New or renamed commands/flags are user-facing API changes; update tests and user docs in the same feature change.
- Keep machine-readable JSON output stable when changing reports.
- For commands that touch real local data, prefer temporary dirs/databases in tests and examples.

## Do not

- Do not make browser/API calls trigger CLI write operations through `serve`.
- Do not run `init --reset`, `vacuum`, or merge/export against a real user database without explicit user approval.
- Do not print raw usage JSON, full private paths, or full session identifiers in new public examples.

## Validation

- Use root Go validation commands.
- For CLI surface changes, run `go run . --help` and `go run . <changed-command> --help`.
- For import/export/merge/report changes, run the relevant `cmd/*_test.go` tests or `go test ./cmd` before broader `go test ./...`.
