# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- Read-only local HTTP API and React/Vite analytics dashboard.
- GitHub Copilot and WorkBuddy usage adapters.
- JSON pricing profiles and read-only estimated-cost reporting.
- `compact-raw` maintenance command for removing historical raw usage evidence.
- Explicit model-resolution, observability, accounting, and request-count coverage fields across CLI reports, API responses, and the Web dashboard.
- Local preview and database-rebuild runbooks covering full page/API validation, replay warning interpretation, consistent backups, atomic replacement, and rollback gates.

### Changed

- Replaced the v1 multi-table ledger with the schema v2 `meta`, `import_runs`, and `usage_events` analytics model.
- Made statistics-only persistence canonical: adapters may use source JSON transiently for parsing and fingerprinting, while new database writes keep `raw_usage_json` as `NULL`.
- Made `status`, `report`, `serve`, and `verify` use strict read-only SQLite paths without implicit configuration or schema writes.
- Changed Codex accounting to reconcile cumulative usage by session, preserve explicit model evidence, and classify missing model evidence as `unknown` instead of guessing a model.
- Increased the Codex import and diagnostics JSONL scanner limit to 64 MiB for large single-line records.

### Fixed

- Hardened export, merge, redaction, source-identity reconciliation, and additive schema validation behavior.
- Removed read-only API head-of-line blocking by allowing a bounded four-connection SQLite pool for concurrent panel aggregations; write paths remain single-connection.
- Filtered Codex fork/subagent parent-prefix and rewritten-burst replay before fingerprinting, with conservative per-child quarantine, replay-local model/timing isolation (including fail-closed attribution when replay timestamps are missing or backward), explicit diagnostic units, size/mtime identity-consistent `doctor codex` policy comparison, and corrected current-ccusage last-or-delta compatibility accounting.
- Preserved and strictly parsed explicit request counts, including zero-token request records, without inferring unknown counts from event or session totals.
- Removed redundant Copilot JSON decoding and a vulnerable Web router dependency.

### Known limitations

- There are no `cleanup`, `restore`, `pricing`, or `workspace` management commands.
- Source-file tracking, parse-error replay, time-filtered or compressed export, currency conversion, and encrypted raw archives remain roadmap items.

## [0.1.0] - 2026-05-23

### Added

- Initial AgentLedger Go CLI implementation.
- SQLite database layer with WAL mode and schema version 1.
- Source adapters for Claude Code, Codex, and Gemini CLI local usage logs.
- Import pipeline with file discovery, parse-time filtering, and grace-period skipping for recently modified files.
- Event fingerprinting with four deterministic strategies: `message_id`, `session_token`, `raw_hash`, and `fallback`.
- Cross-device export and merge using portable `.aldb` SQLite files.
- Reports for daily, weekly, monthly, models, channels, devices, and sessions.
- CLI commands: `init`, `import`, `export`, `merge`, `report`, `status`, `doctor`, `verify`, `vacuum`, and Cobra-generated `completion`.
- TOML configuration with default local paths for database, device id, and agent log sources.
- Fingerprint unit tests and successful `go test ./...` / `go build ./...` validation.
- Public documentation set under `docs/` covering quickstart, CLI, configuration, source adapters, data model, reports, operations, development, and roadmap.

### Known Gaps

- Cleanup/quarantine is present in the design and config shape but not implemented as a CLI command.
- Cost fields exist in the schema, but model pricing estimation is not implemented yet.
- Source file and raw record tracking tables exist in the schema, but the current import path writes normalized `usage_events` only.
