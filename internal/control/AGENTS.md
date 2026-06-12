# internal/control navigation card

`internal/control/` serves the local read-only HTTP API and static Web panel assets. Read this card before modifying `/api/v1/*`, filter parsing, error responses, static file handling, config/status/health snapshots, or path redaction. Key files: `server.go`, `server_test.go`, plus `cmd/serve.go` for listener behavior.

## Local invariants

- API methods are GET-only. Non-GET requests must not mutate local state.
- API responses must not expose `raw_usage_json`; config and DB paths should be home-redacted where intended.
- Filter dates must use `YYYY-MM-DD` or RFC3339 datetime; limits must stay bounded.
- Static serving must prevent path traversal and fall back to `index.html` only inside the selected static root.
- `serve` loopback enforcement lives in `cmd/serve.go`; keep API changes consistent with that local-only trust boundary.

## Local rules

- Backend response shape changes usually require matching updates in `web/src/api/types.ts` and `web/src/api/client.ts`.
- New analytics dimensions or sort modes must be allowlisted in the analytics/report layer.
- Keep placeholder mode working when `web/dist/index.html` is absent.

## Do not

- Do not add POST/PUT/DELETE endpoints for import, merge, vacuum, config writes, or filesystem operations without explicit product/security approval.
- Do not expose full private paths, full raw sessions, or raw envelopes in convenience endpoints.
- Do not bypass static root containment checks for asset serving.

## Validation

- Run `go test ./internal/control ./internal/analytics` for API/server changes.
- If JSON shapes changed, also run `cd web && npm run lint` after dependencies are installed.
- `go run . serve` is a manual runtime check; it binds a loopback port and opens the configured DB.
