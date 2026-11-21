# CLAUDE.md

Guidance for Claude Code when working in this repository. The project layout,
libraries and conventions follow https://github.com/ubie-oss/hecatoncheires.

## Project overview

papers is an AI-native collaborative knowledge canvas (Go backend + React
frontend). Workspaces are defined by configuration files (TOML + Rego); a
**paper** is an ordered list of **blocks**, papers nest through paper blocks,
and a paper is public or private. Writes change the **current state** at once;
a **version** exists only when someone saves one. There are no locks: update
and delete carry the block revision (`rev`) they are based on, and a stale
base whose contents changed is a conflict (409) that the writer resolves or
overrides with `force`. AI agents use the REST API and the MCP endpoint.
Read `docs/model.html` (the source of truth) before changing the model, and
`docs/design.html` before changing the write path or the realtime protocol.

## Commands

- `task dev:server` / `task dev:frontend` – local development (no sign-in)
- `task build` – build the UI and the binary
- `go vet ./...`, `go test ./...` – backend checks (do not use `go build` to check compilation)
- `cd frontend && pnpm lint && pnpm test` – MANDATORY whenever `frontend/` changes
- `cd frontend && pnpm build` – type check + bundle into `frontend/dist`
- `task test:e2e` – Playwright end-to-end tests (`frontend/e2e/`) against a freshly built server; add a test there for every new user-facing flow
- CI (`.github/workflows/ci.yml`) runs all of the above plus the repository contract tests on the Firestore emulator, and fails when `go.mod`/`go.sum` are not tidy

## Architecture

- `pkg/cli/` – CLI (urfave/cli v3) and configuration (`config/`)
- `pkg/controller/http/` – chi router: REST (`api.go`), auth (`auth.go`), WebSocket (`live.go`), MCP (`mcp.go`), embedded SPA
- `pkg/domain/model/` – entities, IDs (`id.go`, UUID v4 in base62), visibility and access (`principal.go`), versions, fractional order keys (`order.go`)
- `pkg/domain/markup/` – the inline markup of block text (Markdown + `[text]{.colour}`); its TypeScript twin is `frontend/src/markup/`, and both run `testdata/markup/cases.json`
- `pkg/domain/interfaces/` – ports: `Repository` (incl. `Watch`), `Tx` (one workspace), `Workspaces`, `AttachmentStore`, `IdentityProvider`
- `pkg/repository/firestore/` – production backend on a Firestore **Enterprise** database (schema: `docs/design.html#schema`). Enterprise creates no index automatically and scans when a query has none, so every new query must add its index to `RequiredIndexes` in `indexes.go`; `pkg/repository/memory/` – dev/tests. Contract tests live in `pkg/repository/*_test.go` and run against both (Firestore when `TEST_FIRESTORE_PROJECT_ID` is set, see `task test:firestore`)
- `pkg/service/google/` – Google OIDC; `pkg/service/workspace/` – workspace configuration and Rego; `pkg/service/attachment/` – image stores (Cloud Storage, directory, memory)
- `pkg/usecase/` – application logic; every method takes the principal and enforces access (`ops.go` batches and conflicts, `version.go`, `tree.go`, `read.go`)
- `pkg/usecase/markdown/` – Markdown <-> blocks
- `frontend/` – React + Vite + TypeScript, embedded via `frontend/static.go`

## Design documents (MANDATORY)

- Every design document in this project is written as a **self-contained HTML page** and published as a claude.ai **artifact**. Never write designs as Markdown.
- `docs/model.html` is the source of truth for the model and concepts (terms, entities and relations, invariants, access, storage layout, operations, conflict rules, versions). When another document or the current code disagrees with it, `docs/model.html` wins; update it first when the model changes.
- `docs/design.html` covers how the model is implemented (write path, versions, tree, feed, auth, API, Firestore layout, search, deployment, scale). Update it (and republish the artifact) whenever the architecture changes. New topic-specific designs go in `docs/<topic>.html` and are published the same way.
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
- Concurrency is revisions, not locks: never add a lock or a write-blocking presence mechanism without a design change. The UI sends the revision an edit started from, not the latest one received. Presence only informs.
- Versions are made only by an explicit save (people or agents). Never save a version automatically.
- IDs are always issued by the server; new blocks, rows and columns are referenced within a batch by `ref`.
- Search goes through `Repository.Search`; repositories maintain the search entries in the same transaction as the blocks. Never search by listing papers or blocks in the use case layer.
- Realtime fan-out comes from the storage change feed (`Repository.Watch`, the short-lived `events` collection). Do not add an application-level pub/sub. Events carry resulting state and must stay idempotent to apply.
- Never write the paper record on every write (Firestore sustains about one write per second per document); content lives on blocks. See `docs/design.html#firestore`.
- Read in batches (`GetPapers`, range reads); never one read per item in a loop. Use-case tests count reads.

### Repository write discipline
- Persist models as-is; no field-by-field copies, no mirror structs, no timestamps stamped in the repository (the use case owns them).
- Every persisted model has `Validate()`, called by the repository before each write.
- Repository tests round-trip every field and run against every backend via `runRepositoryTest`.

### Access
- There are no roles and no system administrators. Workspace access is the workspace's Rego rule (`Workspaces.Allow`), narrowed by the token; paper access is `Principal.SeesPaper` (public, or the creator for private and trashed papers). Callers who may not see something get `ErrNotFound` (do not reveal existence); a read-only token writing gets `ErrPermissionDenied`.
- Only the creator changes visibility or deletes (and only when everything below is theirs); others archive. The home paper cannot be deleted, archived, moved or made private.
- Everything an administrator would decide belongs in the configuration files, never in the UI or API.

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
