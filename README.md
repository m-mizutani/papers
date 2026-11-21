# papers

An AI-native, real-time knowledge canvas. People and AI agents write
**papers** together. A paper is an ordered list of **blocks**: headings, text
with inline formatting and colours, lists, tables, code, images, child papers,
and rich blocks that AI writes (Mermaid diagrams, SVG and sandboxed HTML).

- **Workspaces come from configuration.** One TOML file per workspace, with a
  Rego rule deciding who may use it. There are no roles; a paper is either
  public (everyone in the workspace) or private (its creator).
- **Papers nest.** A child paper is a block in its parent's body. Every
  workspace has a home paper.
- **The current state and versions.** What anyone writes is the current state
  at once. A version (v1, v2, …) exists only when a person or an agent saves
  one. Any version can be viewed, compared and restored.
- **No locks.** Each edit names the revision of the block it was based on. If
  someone else changed that block meanwhile, the whole batch is rejected once
  with the current contents; the writer can redo or overwrite.
- Web UI (React) with live updates and presence over WebSocket; REST API and
  an MCP endpoint (`/mcp`) for AI agents.

[docs/model.html](docs/model.html) is the source of truth for the model.
[docs/design.html](docs/design.html) covers the architecture and the Firestore
layout, [docs/codebase.html](docs/codebase.html) is a tour of the code (all
also published as claude.ai artifacts).

## Run locally

```sh
# terminal 1: API server, no sign-in, in-memory storage, example workspaces
task dev:server
# (= go run . serve --no-authn --ws-origin localhost:5173 \
#      --config examples/config --global-config examples/global.toml)

# terminal 2: Web UI with hot reload
cd frontend && pnpm install && pnpm dev   # http://localhost:5173
```

Or build a single binary that serves the UI itself:

```sh
task build && ./papers serve --no-authn --config examples/config   # http://localhost:8080
```

### Workspaces

```toml
# examples/config/security.toml — one file per workspace
[workspace]
id = "security"           # never change: the storage namespace
name = "Security Team"
description = "Runbooks and incident notes. Write steps as numbered lists."  # agents get this as the workspace's rules
emoji = "🛡️"
language = "ja"

[authz]                   # omit to let every signed-in user in
policy = ["policies/security.rego"]
```

The policy is Rego in package `papers.authz` defining a boolean `allow`; the
input is `{"workspace": {"id"}, "user": {"id", "email", "domain"}}`. Decisions
are cached for 60 seconds; an error denies. Everything is validated at
startup and a bad file stops the server. `--global-config` groups workspaces
in the sidebar (see `examples/global.toml`).

### Connect Claude Code (MCP)

In no-authn mode the MCP endpoint needs no token:

```sh
claude mcp add --transport http papers http://localhost:8080/mcp
```

With sign-in enabled, create an **AI agent** token under *API tokens* in the
UI and pass it as a bearer token (the UI shows the exact command).

## Configuration

| Flag / env | Default | |
|---|---|---|
| `--config` / `PAPERS_CONFIG` | | Directory of workspace definitions (required) |
| `--global-config` | | Display groups of workspaces |
| `--addr` / `PAPERS_ADDR` | `:8080` | Listen address |
| `--base-url` / `PAPERS_BASE_URL` | `http://localhost:8080` | Public URL; OAuth redirect is `<base>/api/auth/callback`; `https` enables Secure cookies |
| `--google-client-id`, `--google-client-secret` | | Google OAuth client |
| `--google-hosted-domain` | | Account chooser hint (not an access control) |
| `--allowed-domain`, `--allowed-email` | | Who may sign in at all (at least one is required with Google); workspaces narrow it further |
| `--session-ttl` | `720h` | Browser session lifetime |
| `--no-authn` | `false` | Development only: every request is the dev user |
| `--enable-mcp` | `true` | Serve `/mcp` |
| `--ws-origin` | | Extra Origin hosts allowed for WebSocket (Vite dev server) |
| `--repository-backend` | `memory` | `firestore` or `memory` (development only) |
| `--firestore-project-id`, `--firestore-database-id` | `(default)` | Firestore **Enterprise edition** database in Native mode. Run `papers firestore setup` once to create its indexes and TTL policies |
| `--firestore-search-language` | (detect) | BCP-47 language code of search queries, such as `ja` |
| `--attachment-backend` | `memory` | Where images go: `gcs`, `dir` or `memory` |
| `--attachment-bucket`, `--attachment-prefix` | | Cloud Storage bucket and prefix for `gcs` |
| `--attachment-dir` | | Directory for `dir` |
| `--max-ops-per-batch` | `300` | Operations per request (blocks per batch are capped at 240) |
| `--paper-touch-interval` | `1m` | How stale a paper's "updated" time may get before a write refreshes it |
| `--trash-retention` | `720h` | How long papers stay in the trash |
| `--job-inline` | `3s` | How long a request runs the background job it starts (trash, restore or archive of a subtree, moves across workspaces) before answering `202` |
| `--job-lease`, `--job-poll` | `1m`, `15s` | When an instance stops, another takes its jobs over after the lease; `job-poll` is how often instances look |
| `--job-step` | `200` | Papers (or blocks of a copy) per transaction of a background job |

Background jobs continue on the instance after the response, so on Cloud Run allocate CPU always (`--no-cpu-throttling`); otherwise they still finish, but only when an instance looks for them.

All flags also read `PAPERS_*` environment variables (see `papers serve --help`).
