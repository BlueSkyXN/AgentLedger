# Changelog

All notable changes to this project will be documented in this file.

## [0.1.0] - 2026-05-23

### Added

- Initial AgentLedger Go CLI implementation.
- SQLite database layer with WAL mode and schema version 1.
- Source adapters for Claude Code, Codex, Gemini CLI, and Qwen local usage logs.
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
