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
Read `docs/model.md` (the source of truth) before changing the model, and
`docs/design.md` before changing the write path or the realtime protocol.

## Commands

- `task dev:server` / `task dev:frontend` – local development (no sign-in)
- `task build` – build the UI and the binary
- `go vet ./...`, `go test ./...` – backend checks (do not use `go build` to check compilation)
- `cd frontend && pnpm lint && pnpm test` – MANDATORY whenever `frontend/` changes
- `cd frontend && pnpm build` – type check + bundle into `frontend/dist`
- `task test:e2e` – Playwright end-to-end tests (`frontend/e2e/`) against a freshly built server; add a test there for every new user-facing flow
- `golangci-lint run` (`.golangci.yml`), `cd frontend && pnpm typecheck` – lint and type checks
- CI runs one workflow per concern in `.github/workflows/`: `go-test.yml` (race tests, and the repository contract tests on the Firestore emulator), `ui-test.yml` (Vitest), `e2e.yml` (Playwright), `lint.yml` (golangci-lint, go vet, go mod tidy, ESLint, TypeScript), `security.yml` (govulncheck, gosec, Trivy, pnpm audit; also weekly), `build.yml` (UI, binary) and `docker.yml` (Docker image: build only on pull requests, build and push to GHCR on `main`). It fails when `go.mod`/`go.sum` are not tidy

## Architecture

- `pkg/cli/` – CLI (urfave/cli v3) and configuration (`config/`)
- `pkg/controller/http/` – chi router: REST (`api.go`), auth (`auth.go`), WebSocket (`live.go`), MCP (`mcp.go`), embedded SPA
- `pkg/domain/model/` – entities, IDs (`id.go`, UUID v4 in base62), visibility and access (`principal.go`), versions, fractional order keys (`order.go`)
- `pkg/domain/markup/` – the inline markup of block text (Markdown + `[text]{.colour}`); its TypeScript twin is `frontend/src/markup/`, and both run `testdata/markup/cases.json`
- `pkg/domain/interfaces/` – ports: `Repository` (incl. `Watch`), `Tx` (one workspace), `Workspaces`, `AttachmentStore`, `IdentityProvider`
- `pkg/repository/firestore/` – production backend on a Firestore **Enterprise** database (schema: `docs/design.md#firestore`). Enterprise creates no index automatically and scans when a query has none, so every new query must add its index to `RequiredIndexes` in `indexes.go`; `pkg/repository/memory/` – dev/tests. Contract tests live in `pkg/repository/*_test.go` and run against both (Firestore when `TEST_FIRESTORE_PROJECT_ID` is set, see `task test:firestore`; search needs a real Enterprise database and runs only with `TEST_FIRESTORE_ENTERPRISE`)
- `pkg/service/google/` – Google OIDC; `pkg/service/workspace/` – workspace configuration and Rego; `pkg/service/attachment/` – image stores (Cloud Storage, directory, memory)
- `pkg/usecase/` – application logic; every method takes the principal and enforces access (`ops.go` batches and conflicts, `version.go`, `tree.go`, `read.go`)
- `pkg/usecase/markdown/` – Markdown <-> blocks
- `pkg/usecase/sample/` – the sample papers demo mode (`serve --demo`) writes into empty workspaces; `usecase/sample.go` writes them
- `frontend/` – React + Vite + TypeScript, embedded via `frontend/static.go`

## Documents (MANDATORY)

- Documents in the repository are **Markdown only**, in English. Diagrams are Mermaid code blocks (or plain text) so GitHub renders them; no HTML pages, no images of diagrams.
- Designs under discussion are shown as specs on claude.ai, not committed. Only what was decided goes into `docs/`.
- `docs/model.md` is the source of truth for the model and concepts (terms, entities and relations, invariants, access, storage layout, operations, conflict rules, versions). When another document or the current code disagrees with it, `docs/model.md` wins; update it first when the model changes.
- `docs/design.md` covers how the model is implemented (write path, versions, tree, jobs, feed, auth, API, Firestore layout, search, deployment, testing, scale); update it whenever the architecture changes. `docs/codebase.md` is a map of the code; keep it in step with the layout.

## Demo samples (MANDATORY)

`papers serve --demo` (demo mode; not the in-browser demo of `frontend/src/mock/`)
fills each workspace that is still empty with the sample papers defined in
`pkg/usecase/sample/`. Keep them showing what papers can do:

- When you add a block type, a paper kind, a markup feature or anything else that
  changes what stored content looks like, add it to a sample in the same change.
  `TestSamplesCoverBlockTypes` fails while a block type has no sample.
- When you change or remove a feature a sample shows or explains, update that sample.
- Samples are made up, written in English, and self-contained: images are files in
  `pkg/usecase/sample/` stored as attachments, and HTML loads nothing from other hosts.
- Samples are written by the system actor through the normal write path. Never save
  a version for them and never add a marker that tells samples from other papers.

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
- Never write the paper record on every write (Firestore sustains about one write per second per document); content lives on blocks. See `docs/design.md#firestore`.
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
- AI-authored HTML/SVG renders only inside the sandboxed `RichFrame` (blocks) or `HtmlPage` (HTML papers), never with `allow-same-origin`. Never inject block content as HTML elsewhere.

### Screen changes and screenshots (MANDATORY)
- Any change that alters what a screen shows (components, styles, tokens, layout, i18n copy) must be checked with screenshots taken by Playwright, and you must look at them before you report the work done. Passing tests and type checks are not a substitute.
- Capture against the real app: build the UI (`cd frontend && pnpm build`), run the server the way `frontend/playwright.config.ts` does (`go run . serve --no-authn --addr 127.0.0.1:18090 --base-url http://127.0.0.1:18090 --config examples/config --global-config examples/global.toml`), and drive it with Playwright (`page.screenshot`). Cover every screen and state the change touches, at a desktop width and at a phone width, in light and dark themes when colours change.
- Save the captures under `frontend/pr-screenshots/` (ignored by git). Never commit a screenshot or any image made for review: not in `frontend/`, `docs/`, `testdata/` or anywhere else in the repository. Keep a throwaway capture script outside the repository too, unless it becomes an e2e test.
- Put the screenshots in the pull request body. Upload them with the `gh` command as assets of the `pr-screenshots` prerelease (create it once with `gh release create pr-screenshots --prerelease --title "PR screenshots" --notes "Screenshots for pull requests; not a release."`), naming each file `<branch>-<screen>-<state>.png` (`/` in the branch name replaced by `-`) so pull requests do not overwrite each other: `gh release upload pr-screenshots frontend/pr-screenshots/<branch>-*.png --clobber` (only this branch's files, so another pull request's images are not replaced). Embed each one in the body as `![<screen, state>](https://github.com/m-mizutani/papers/releases/download/pr-screenshots/<file>)`.
- If uploading with `gh` is not possible (the command is missing, not authenticated or refused), publish an Artifact on claude.ai that shows the screenshots with their screen and state, share it publicly, and put its URL in the pull request body instead. Say in the body which of the two was used.

### Language and commits
- Code comments and string literals in English (UI copy via i18n).
- Commit messages: single line, Semantic Commit format (`feat: ...`).
