# CLAUDE.md

Guidance for Claude Code when working in this repository. The project layout,
libraries and conventions follow https://github.com/ubie-oss/hecatoncheires.

## Project overview

papers is an AI-native collaborative knowledge canvas (Go backend + React
frontend). A **paper** is an ordered list of **blocks**; every change is a
**commit**; humans hold **soft locks** on the block they edit; writes are
last-writer-wins per block. AI agents use the REST API and the MCP endpoint.
Read `docs/design.md` before changing the data model or the realtime protocol.

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
- `pkg/domain/interfaces/` – ports: `Repository`, `PaperTx`, `EventBus`, `IdentityProvider`
- `pkg/repository/memory/` – in-memory backend (dev/tests). Contract tests live in `pkg/repository/*_test.go`
- `pkg/service/google/` – Google OIDC
- `pkg/usecase/` – application logic; every method takes the principal and enforces authorization
- `pkg/usecase/markdown/` – Markdown <-> blocks
- `frontend/` – React + Vite + TypeScript, embedded via `frontend/static.go`

## Rules (inherited from hecatoncheires)

### Errors and logging
- Use `github.com/m-mizutani/goerr/v2`; wrap with context (`goerr.V`). Never swallow errors.
- Discriminate errors with `errors.Is/As` against the sentinels in `pkg/domain/model/errors.go`; never match on `err.Error()`.
- Non-fatal errors go through `errutil.Handle(ctx, err, msg)`.
- Never call global `slog.*`; use `logging.From(ctx)`.
- Close resources with `safe.Close(ctx, c)`; launch goroutines with `async.Dispatch*`.

### Stateless, multi-instance design
- The server runs as several Cloud Run instances. Never keep cross-request state in memory. Per-connection state (one WebSocket) is fine.
- Realtime fan-out goes through `interfaces.EventBus`; the memory bus is single-instance only.

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
