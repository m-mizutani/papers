# papers

An AI-native, real-time knowledge canvas. People and AI agents write
**papers** together: a paper is an ordered list of **blocks** (headings,
text, lists, tables, code, and rich blocks such as Mermaid diagrams, SVG and
sandboxed HTML that AI writes). Every change is a **commit**; a human editing
a block holds a soft lock on it, so AI and other people see who is typing
where. Writes are last-writer-wins per block, and overwrites are recorded.

- Web UI (React) with live updates over WebSocket
- REST API and an MCP endpoint (`/mcp`) for AI agents
- Google sign-in with papers-issued user IDs, workspaces, groups and
  fine-grained roles (workspace roles, per-paper restrictions, token scopes)

See [docs/design.html](docs/design.html) for the architecture and the
Firestore schema (also published as a claude.ai artifact).

## Run locally

```sh
# terminal 1: API server, no sign-in, in-memory storage
go run . serve --no-authn --ws-origin localhost:5173

# terminal 2: Web UI with hot reload
cd frontend && pnpm install && pnpm dev   # http://localhost:5173
```

Or build a single binary that serves the UI itself:

```sh
task build && ./papers serve --no-authn   # http://localhost:8080
```

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
| `--addr` / `PAPERS_ADDR` | `:8080` | Listen address |
| `--base-url` / `PAPERS_BASE_URL` | `http://localhost:8080` | Public URL; OAuth redirect is `<base>/api/auth/callback`; `https` enables Secure cookies |
| `--google-client-id`, `--google-client-secret` | | Google OAuth client |
| `--google-hosted-domain` | | Account chooser hint (not an access control) |
| `--allowed-domain`, `--allowed-email` | | Who may sign in (at least one is required with Google) |
| `--admin-email` | | System administrators |
| `--session-ttl` | `720h` | Browser session lifetime |
| `--no-authn` | `false` | Development only: every request is the dev user (an admin) |
| `--lock-ttl` | `30s` | Soft lock lifetime without heartbeat |
| `--workspace-creation` | `everyone` | `everyone` or `admin` |
| `--enable-mcp` | `true` | Serve `/mcp` |
| `--ws-origin` | | Extra Origin hosts allowed for WebSocket (Vite dev server) |
| `--repository-backend` | `memory` | `firestore` or `memory` (development only) |
| `--firestore-project-id`, `--firestore-database-id` | `(default)` | Firestore database (see [docs/design.html](docs/design.html) for indexes and TTL) |
| `--paper-touch-interval` | `1m` | How stale a paper's "updated" time may get before a commit refreshes it |

All flags also read `PAPERS_*` environment variables (see `papers serve --help`).
