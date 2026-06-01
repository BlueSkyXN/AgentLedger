# web navigation card

`web/` is the React 18 + Vite read-only analysis panel. Read this card before modifying frontend API types/client code, routes/pages, filters, charts, theme, or build configuration. Key files: `package.json`, `vite.config.ts`, `src/api/`, `src/hooks/`, `src/pages/`, `src/styles.css`.

## Local invariants

- The panel reads only from `/api/v1/*`; it must not access SQLite files or local agent logs directly.
- Frontend types in `src/api/types.ts` must match `internal/control` JSON responses.
- Vite dev proxy points `/api` to `http://127.0.0.1:54217`; production assets are served by the Go binary from `web/dist`.
- Do not display `raw_usage_json`. Treat paths, sessions, model names, and token aggregates as private local usage data.

## Local rules

- `npm run lint` is TypeScript checking, not ESLint.
- Keep `package-lock.json` under npm control. Do not hand-edit lockfile entries.
- If adding dependencies, prefer existing React, React Query, ECharts, and utility code first.
- UI filters should stay aligned with backend filters: `since`, `until`, `channel`, `provider`, `model`, `session`.

## Do not

- Do not add buttons that call import, merge, vacuum, reset, config write, or filesystem operations.
- Do not hardcode sample private paths, real session IDs, real project names, or copied raw telemetry in UI fixtures.
- Do not edit `web/dist/`, `.vite/`, `node_modules/`, or `*.tsbuildinfo`.

## Validation

- After frontend code changes, run `cd web && npm run lint`.
- For build or asset-serving changes, run `cd web && npm run build`.
- If backend API shapes changed, validate the matching Go tests for `internal/control` as well.
