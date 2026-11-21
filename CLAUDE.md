# CLAUDE.md

Guidance for Claude Code when working in this repository. The project layout,
libraries and conventions follow https://github.com/ubie-oss/hecatoncheires.

## Project overview

papers is an AI-native collaborative knowledge canvas (Go backend + React
frontend). A **paper** is an ordered list of **blocks**; every change is a
**commit**. There are no locks: update, delete and restore carry the block
version they are based on, a stale base whose contents changed is a conflict
(409) that the writer resolves or overrides with `force` (last writer wins,
recorded). AI agents use the REST API and the MCP endpoint.
Read `docs/design.html` before changing the data model or the realtime protocol.

## Commands

- `task dev:server` / `task dev:frontend` – local development (no sign-in)
- `task build` – build the UI and the binary
- `go vet ./...`, `go test ./...` – backend checks (do not use `go build` to check compilation)
- `cd frontend && pnpm lint && pnpm test` – MANDATORY whenever `frontend/` changes
- `cd frontend && pnpm build` – type check + bundle into `frontend/dist`

## Architecture

- `pkg/cli/` – CLI (urfave/cli v3) and configuration (`config/`)
- `pkg/controller/http/` – chi router: REST (`api.go`), auth (`auth.go`), WebSocket (`live.go`), MCP (`mcp.go`), embedded SPA
- `pkg/domain/model/` – entities, roles and authorization (`principal.go`), fractional order keys (`order.go`)
- `pkg/domain/interfaces/` – ports: `Repository` (incl. `WatchPaper`), `PaperTx`, `IdentityProvider`
- `pkg/repository/firestore/` – production backend on a Firestore **Enterprise** database (schema: `docs/design.html#schema`). Enterprise creates no index automatically and scans when a query has none, so every new query must add its index to `RequiredIndexes` in `indexes.go`; `pkg/repository/memory/` – dev/tests. Contract tests live in `pkg/repository/*_test.go` and run against both (Firestore when `TEST_FIRESTORE_PROJECT_ID` is set, see `task test:firestore`)
- `pkg/service/google/` – Google OIDC
- `pkg/usecase/` – application logic; every method takes the principal and enforces authorization
- `pkg/usecase/markdown/` – Markdown <-> blocks
- `frontend/` – React + Vite + TypeScript, embedded via `frontend/static.go`

## Design documents (MANDATORY)

- Every design document in this project is written as a **self-contained HTML page** and published as a claude.ai **artifact**. Never write designs as Markdown.
- `docs/design.html` is the single source of truth for the overall design. Update it (and republish the artifact) whenever a decision, the data model, the schema, the protocol or the API changes. New topic-specific designs go in `docs/<topic>.html` and are published the same way.
- Follow the artifact page rules: one HTML file, inline CSS/JS, diagrams as inline SVG (no Mermaid), light and dark themes, readable on phones. Put decisions first, with dates, and keep an "open questions" section.

## Rules (inherited from hecatoncheires)

### Errors and logging
- Use `github.com/m-mizutani/goerr/v2`; wrap with context (`goerr.V`). Never swallow errors.
- Discriminate errors with `errors.Is/As` against the sentinels in `pkg/domain/model/errors.go`; never match on `err.Error()`.
- Non-fatal errors go through `errutil.Handle(ctx, err, msg)`.
- Never call global `slog.*`; use `logging.From(ctx)`.
- Close resources with `safe.Close(ctx, c)`; launch goroutines with `async.Dispatch*`.

### Stateless, multi-instance design
- The server runs as several Cloud Run instances. Never keep cross-request state in memory. Per-connection state (one WebSocket) is fine.
- Concurrency is versions, not locks: never add a lock or a write-blocking presence mechanism without a design change. The UI sends the version an edit started from, not the latest one received (`docs/concurrency.html`).
- Search goes through `Repository.Search`; repositories maintain the search entries in the same transaction as the blocks. Never search by listing papers or blocks in the use case layer.
- Realtime fan-out comes from the storage change feed (`Repository.WatchPaper`, Firestore listeners). Do not add an application-level pub/sub. Events carry resulting state and must stay idempotent to apply.
- Never write the paper record on every commit (Firestore sustains about one write per second per document); history lives on blocks. See `docs/design.html#schema`.

### Repository write discipline
- Persist models as-is; no field-by-field copies, no mirror structs, no timestamps stamped in the repository (the use case owns them).
- Every persisted model has `Validate()`, called by the repository before each write.
- Repository tests round-trip every field and run against every backend via `runRepositoryTest`.

### Authorization
- All access decisions go through `Principal.WorkspaceRole` / `Principal.PaperRole`. Callers without read access get `ErrNotFound` (do not reveal existence); with read but not enough role, `ErrPermissionDenied`.
- Tokens are capped by scope (read → viewer, write → editor) and can never administer.

### Tests
- Write tests for all code. Test packages are `<pkg>_test`; `xyz.go` → `xyz_test.go`. Use `export_test.go` for test-only seams.
- Use `github.com/m-mizutani/gt`. `t.Skip` only for missing environment variables.

### Frontend
- No hardcoded colours, spacing or sizes outside the tokens in `frontend/src/styles/global.css`.
- All user-facing text goes through i18n (`frontend/src/i18n/keys.ts`, `en.ts`, `ja.ts`).
- Enter-key handlers must respect IME composition: use `isEnter` / `isImeComposing` from `src/utils/keyboard.ts` (enforced by ESLint).
- AI-authored HTML/SVG renders only inside the sandboxed `RichFrame` (no `allow-same-origin`). Never inject block content as HTML elsewhere.

### Language and commits
- Code comments and string literals in English (UI copy via i18n).
- Commit messages: single line, Semantic Commit format (`feat: ...`).
