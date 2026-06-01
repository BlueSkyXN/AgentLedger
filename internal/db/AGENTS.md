# internal/db navigation card

`internal/db/` owns SQLite schema v2, connection setup, event upsert, import run bookkeeping, stats, export, and merge operations. Read this card before any schema, compatibility, redaction, merge, or upsert-completeness change. Key files: `schema.go`, `db.go`, `ops.go`, and matching tests.

## Why this is high-risk

- The configured database can contain private local usage history, source paths, session IDs, and raw usage envelopes.
- Schema changes can make existing v2 databases unreadable or silently misreported.
- Export and merge operate on SQLite files and can leak or corrupt user data if validation/redaction regresses.

## Required before changes

- Read `docs/data-model.md` and `docs/privacy-and-operations.md` for the current data/privacy contract.
- Confirm whether a change needs compatibility migration, test fixture updates, and docs updates.
- Use temporary databases in tests; do not point tests or smoke commands at `local/data/agent-ledger.db`.

## Do not

- Do not reintroduce v1 ledger tables or conflict/device history without an explicit schema design.
- Do not drop existing columns or change `SchemaVersion` semantics without a migration plan.
- Do not weaken export redaction of `project_path`, `source_file`, `raw_usage_json`, or import warning text.
- Do not accept non-SQLite, directory, or wrong-schema inputs in merge/export paths.

## Validation

- For DB changes, run `go test ./internal/db ./cmd`.
- For export/merge/privacy behavior, include the relevant `cmd/export_test.go`, `cmd/import_test.go`, and merge/db tests.
- Finish with `go test ./...` when the Go/CGO toolchain and module cache are available.
