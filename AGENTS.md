# Repository agent instructions

## Purpose

AgentLedger 是本地优先的 AI Coding Agent usage analytics 工具：Go CLI 负责导入 Claude Code、Codex、GitHub Copilot、Gemini CLI 等本机日志到 SQLite，生成报表并提供只读 HTTP API；`web/` 是 React/Vite 只读分析面板。

## Codex startup behavior

- Codex 通常从仓库根目录启动，本文件是 repo-local 启动期主规则。
- 子目录 `AGENTS.md` 是按需导航卡片，不会在根启动时自动进入上下文。
- 修改带有本地 `AGENTS.md` 的目录前，必须先按下方目录地图读取对应文件。
- 如果一次修改跨多个带本地卡片的目录，先读取所有相关卡片，再做实现计划。
- 如果从子目录启动，路径链上的本地 `AGENTS.md` 可能自动加载；仍以本文件作为根启动 workflow 的 router。

## Directory map

| Path | Responsibility | Local AGENTS.md | Read when |
|---|---|---:|---|
| `cmd/` | Cobra CLI command surface: `init`, `import`, `export`, `merge`, `report`, `serve`, maintenance commands | Yes | 修改命令、flag、stdout/stderr、退出语义、会读写数据库的 CLI 流程前 |
| `internal/adapters/` | 本机 agent 日志 discovery、JSON/JSONL parser、token/timing normalization | Yes | 新增或修改 Claude/Codex/Copilot/Gemini adapter、usage envelope、dedupe 输入字段前 |
| `internal/analytics/` | 只读 SQL 聚合，供 API 和 Web 面板使用 | No | 根规则已覆盖；改 filter、breakdown、sort、limit 时同步检查 `internal/control/` 和 `web/` |
| `internal/config/` | TOML config、默认路径、`~` 展开、agent 配置 | No | 根规则已覆盖；改配置字段时检查 docs 和 API config snapshot 兼容性 |
| `internal/control/` | 本机 HTTP server、只读 `/api/v1/*`、静态面板托管、路径脱敏 | Yes | 修改 API endpoint、filter parsing、static serving、config/status/health response 前 |
| `internal/db/` | SQLite schema v2、连接参数、event upsert、merge/export/stat ops | Yes | 修改 schema、migration/compatibility、export redaction、merge、upsert 完整度规则前 |
| `internal/fingerprint/` | 稳定 event fingerprint 与 raw JSON canonicalization | No | 根规则已覆盖；改 fingerprint 会影响去重和 merge，需同时检查 adapter/db 测试 |
| `internal/model/` | 跨 package 共享 domain structs 和 token helper | No | 根规则已覆盖；字段变更会连带 adapter/db/report/API/Web 类型 |
| `internal/report/` | CLI text/JSON reports 和 report SQL | No | 根规则已覆盖；改 report filters、`--by`、`slow --sort` 时使用 allowlist 并检查 CLI 输出 |
| `web/` | React/Vite 只读分析面板，npm package 在该目录内 | Yes | 修改前端 API client/types、页面、筛选器、chart、build 配置或样式前 |
| `docs/` | 面向用户的中文文档 | No | 文档跟随代码事实更新；不要把未实现功能写成已实现 |
| `testdata/` | 测试 fixture 位置，目前没有 tracked 文件 | No | 添加 fixture 前确保不包含真实 session、路径、raw usage 或私有数据 |
| `local/` | `.gitignore` 排除的私有 notes、实验、数据库、导出和本机材料 | No | 默认不要修改；只有用户明确要求处理本机私有材料时才读取或写入 |
| `.codex/`, `.playwright-mcp/` | `.gitignore` 排除的本机 agent/工具入口 | No | 不作为项目源码维护，不要提交 |
| `main.go`, `go.mod`, `go.sum` | Go module 入口和依赖锁定 | No | 变更依赖或 module 入口时按 Go 验证流程处理 |
| `README.md`, `CHANGELOG.md`, `LICENSE` | 根文档和许可证 | No | README/CHANGELOG 只在用户或代码变更需要时更新；不要改许可证 |

## On-demand cat protocol

Before editing files under a directory that has a local `AGENTS.md`, read that file first:

```bash
cat <path>/AGENTS.md
```

If multiple nested `AGENTS.md` files exist on the path to the target file, read them from shallow to deep. After reading a local card, apply the more specific local rule when it conflicts with this root file. If a directory has no local card, follow this root file and inspect the nearest relevant docs/source files before making assumptions.

## Commands

Commands below are confirmed from `README.md`, `docs/development.md`, `go.mod`, `web/package.json`, and `.github/workflows/ci.yml`. There is no root `package.json` and no `Makefile`.

| Command | Purpose | Scope | Sandbox notes |
|---|---|---|---|
| `go test ./...` | Run Go unit tests | repo | Requires Go compatible with `go.mod` (`go 1.26.3` here), CGO, and a C toolchain for `github.com/mattn/go-sqlite3`; may need module cache or network if dependencies are absent |
| `go build ./...` | Build all Go packages | repo | Same Go/CGO/toolchain notes as tests |
| `go vet ./...` | Static Go vet checks | repo | Same Go/CGO/toolchain notes as tests |
| `gofmt -l .` | List Go files that need formatting | repo | OK in sandbox; does not modify files |
| `go run . --help` | Smoke-test root CLI help | repo | Opens no server; may compile with CGO |
| `go run . <command> --help` | Smoke-test a changed command surface | repo | Use the specific command touched, for example `report monthly --help` |
| `mkdir -p bin && go build -o bin/agent-ledger .` | Build local binary | repo | Writes ignored `bin/`; do not commit build artifacts |
| `go run . serve` | Start local read-only panel/API | repo | Manual/runtime check; binds loopback, opens configured SQLite DB, and keeps a server process running |
| `go run . doctor` | Inspect config paths and enabled agent source discovery | repo | Reads local config and scans configured paths; do not paste private paths/session data into public output |
| `go run . verify` | Run SQLite integrity check on configured DB | repo | Reads configured DB; requires local database to exist |
| `cd web && npm ci` | Install locked Web dependencies | `web/` | Requires Node.js `^20.19.0` or `>=22.12.0` and network unless npm cache is warm; rewrites ignored `node_modules` from `package-lock.json` |
| `cd web && npm run lint` | TypeScript checks for app and Vite config | `web/` | Requires dependencies installed; script is `tsc --noEmit`, not ESLint |
| `cd web && npm run build` | TypeScript checks plus Vite production build | `web/` | Requires dependencies installed; writes ignored `web/dist/` |
| `cd web && npm run dev` | Vite dev server with `/api` proxy to `127.0.0.1:54217` | `web/` | Manual/runtime check; requires backend server for API data |
| `cd web && npm run preview` | Preview built Web panel | `web/` | Requires prior build |

## Global rules

- Keep the repository local-first. Do not add network calls, telemetry, hosted services, remote sync, or external API dependencies unless the user explicitly asks and the privacy model is updated.
- Treat local agent logs, SQLite databases, `.aldb` exports, `raw_usage_json`, session IDs, request IDs, message IDs, source file paths, project paths, screenshots, and panel exports as private user data.
- Do not copy real local logs, real database rows, raw usage envelopes, private paths, tokens, or credentials into commits, docs, test snapshots, PR text, screenshots, or public examples.
- v2 schema is intentionally small: `meta`, `import_runs`, `usage_events`. Do not reintroduce v1 source/observation/conflict/device ledger tables without an explicit schema design and regression plan.
- Token fields must come from explicit source usage envelopes or documented adapter-specific fallback. Do not infer token counts from text length, neighboring timestamps, file order, or UI display text.
- Timing fields must stay `NULL` unless the source explicitly provides enough timing boundaries. Do not synthesize TTFT, total duration, or output TPS from unrelated timestamps.
- SQL that uses user-controlled report/API dimensions, sort keys, or bucket names must use allowlists. Use query parameters for values; do not concatenate raw user input into SQL expressions.
- `serve` is read-only in this release. Browser/API paths must not trigger `import`, `merge`, `vacuum`, `init --reset`, config writes, file writes, or source log mutation.
- `serve` must remain loopback-only unless there is a clear product/security decision to add authentication and remote access.
- Export defaults must preserve privacy: redacted export should clear private paths and raw usage fields as current code does. Unredacted export must be an explicit configuration choice.
- Adapters must preserve source boundaries: discover configured paths, parse supported local file formats, normalize into `ParsedRecord`, and let fingerprint/db layers handle dedupe/upsert.
- When adding or changing an adapter, use synthetic fixtures or carefully redacted samples. Do not commit real user logs or copied private session files.
- CLI command output is user-facing. Keep text output stable unless the task is explicitly changing UX; update tests/docs when changing command names, flags, JSON shape, or report columns.
- Frontend API types in `web/src/api/types.ts` must match `internal/control` JSON responses. Backend API changes and frontend type/client changes should be coordinated.
- `local/`, `.codex/`, `.playwright-mcp/`, `bin/`, `web/dist/`, database files, `.aldb`, `node_modules`, and build outputs are ignored local artifacts. Do not move them into tracked paths.
- Use existing dependencies and standard library first. New Go or npm dependencies must be justified by a concrete capability gap.
- Go changes should be gofmt-clean. Prefer small package-level tests near changed behavior.

## Do not

- Do not run `agent-ledger init --reset`, `vacuum`, destructive cleanup, or database overwrite commands against a real user database without explicit user approval.
- Do not expose `serve` on `0.0.0.0`, LAN IPs, public tunnels, or reverse proxies as part of routine development.
- Do not add browser-triggered import/merge/vacuum/config mutation endpoints without a product decision and security review.
- Do not sum `source_total_tokens` for reports; it is a raw source diagnostic field, not the canonical event total.
- Do not make `raw_usage_json`, `source_file`, `project_path`, or full `session_id` casually visible in public-facing examples.
- Do not edit ignored `local/` materials unless the user specifically asks. They may contain real databases and private reports.
- Do not hand-edit generated or build output such as `web/dist/`, `web/*.tsbuildinfo`, `internal/control/embed/dist/`, `bin/`, or coverage/profile files.
- Do not convert the repo to a monorepo package manager layout; Go is rooted at repo root and npm is scoped to `web/`.
- Do not invent validation commands. If a command is not in repo config/docs or not a standard Go/npm command for the existing module, mark it as unconfirmed.

## Validation

Choose validation based on the files changed and the risk of the behavior:

1. Documentation-only or `AGENTS.md`-only changes: inspect the diff for accuracy and scope. No Go/npm tests are required unless the text claims behavior that needs live verification.
2. Go formatting-sensitive changes: run `gofmt -l .`; if it prints files you touched, format them before finishing.
3. General Go code changes: run `go test ./...`, `go vet ./...`, and `go build ./...` when the local Go/CGO/toolchain/module cache allow it.
4. CLI surface changes: run `go run . --help` plus `go run . <changed-command> --help`. For report behavior, add or run focused Go tests around `cmd/` and `internal/report/`.
5. Adapter/import/fingerprint changes: run `go test ./internal/adapters ./internal/fingerprint ./cmd` at minimum, then `go test ./...` for final confidence.
6. SQLite schema, export, merge, or upsert changes: run `go test ./internal/db ./cmd`, then `go test ./...`. Check privacy redaction tests when export behavior changes.
7. API/analytics changes: run `go test ./internal/control ./internal/analytics`, then coordinate Web type/client changes and run Web validation if response shapes changed.
8. Web changes: from `web/`, run `npm run lint` and `npm run build` after dependencies are installed.
9. Runtime checks such as `go run . serve`, `go run . doctor`, and `go run . verify` depend on local config/database and may reveal private paths or usage data. Use them only when relevant, and summarize private output carefully.

If a validation step cannot be run because Go, CGO, npm dependencies, network, database, or a long-running server is unavailable, state exactly what was skipped and why.

## Notes for future agents

- The docs are mostly Chinese; keep repository-facing prose in Chinese unless a specific file already uses English.
- `web/package.json` has `lint` but no frontend test script. Do not report `npm test` as available.
- `.github/workflows/ci.yml` runs the documented Go checks plus Web audit/typecheck/build on pull requests and pushes to `main`; do not claim additional CI coverage that is not present in that workflow.
- This repository previously had closeout work around export/import hardening and Codex token accounting. Re-verify current code before relying on those historical facts.
