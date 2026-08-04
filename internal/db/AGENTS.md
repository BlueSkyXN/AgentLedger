# internal/db navigation card

`internal/db/` owns SQLite schema v3, identity-version gate, event reconcile, import run bookkeeping, stats, export, and merge operations. Read this card before any schema, redaction, merge, or reconcile change. Key files: `schema.go`, `db.go`, `ops.go`, and matching tests.

## Why this is high-risk

- The configured v3 database can contain private local usage history, source/project paths, session IDs, and warning summaries; legacy v2 backups may additionally contain raw usage envelopes.
- Schema changes can make existing v3 databases unreadable or silently misreported; v2 remains explicitly unsupported.
- Export and merge operate on SQLite files and can leak or corrupt user data if validation/redaction regresses.

## Required before changes

- Read `docs/data-model.md` and `docs/privacy-and-operations.md` for the current data/privacy contract.
- Confirm whether a change needs compatibility migration, test fixture updates, and docs updates.
- Use temporary databases in tests; do not point tests or smoke commands at `local/data/agent-ledger.db`.

## Do not

- Do not reintroduce v1 ledger tables or conflict/device history without an explicit schema design.
- Do not drop existing columns or change `SchemaVersion` semantics without a migration plan.
- Do not weaken export redaction of `project_path`, `source_file`, or import warning text.
- Do not accept non-SQLite, directory, or wrong-schema inputs in merge/export paths.

## Validation

- For DB changes, run `go test ./internal/db ./cmd`.
- For export/merge/privacy behavior, include the relevant `cmd/export_test.go`, `cmd/import_test.go`, and merge/db tests.
- Finish with `go test ./...` when the Go/CGO toolchain and module cache are available.
