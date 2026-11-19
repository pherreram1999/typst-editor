# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Typst Editor: real-time collaborative Typst editor. Svelte 5 SPA (`frontend/`), Go `net/http` JSON + WebSocket API (`backend/`), MariaDB (`database/`). Features: multi-user editing via OT (CodeMirror 6 + `@codemirror/collab`), live SVG preview and PDF viewer produced by the system `typst` binary, manual GitHub pull/push via the system `git` binary, project sharing (owner invites registered users as members), JWT auth and admin user management. All user-facing text, error messages, log lines and code comments are in **Spanish** — keep it that way.

## Commands

Backend (run from `backend/` — config loads `.env` from the current directory):
```bash
go run .                         # start API on :8080 (same as `go run . serve`)
go run . key:generate            # write a new JWT_SECRET into .env
go build -o bin/server .         # build binary (bin/ is gitignored)
go vet ./...
go test ./...                    # unit tests (collab changesets) + git push/pull integration test
go test ./internal/collab -run TestApplyChangeSet
```

Frontend (from `frontend/`):
```bash
npm run dev       # Vite on :5173, proxies /api (incl. WebSocket, ws: true) -> http://localhost:8080
API_TARGET=http://127.0.0.1:18080 npm run dev   # proxy (dev and preview) to another backend
npm run build
```

Database bootstrap (once): `sudo mariadb < database/01_setup.sql`. Runtime needs `typst` and `git` on PATH (or `TYPST_BIN`/`GIT_BIN`); project data lives in `DATA_DIR` (default `backend/data/`, gitignored). No frontend tests or linters are configured.

## Architecture

### Backend
- `main.go` sits at the `backend/` root (no `cmd/` dir) and dispatches subcommands. Module path `typsteditor/backend`; packages under `internal/`.
- `internal/config` — the DSN forces `loc=UTC` and session `time_zone='+00:00'` so TIMESTAMPs serialize correctly regardless of the DB server's zone; keep both if you touch the DSN. Hand-rolled `.env` loader (doesn't override real env vars). Startup fails if `JWT_SECRET` < 32 chars.
- `internal/database` — `Migrate` runs the `schema` statements (`users`, `github_accounts`, `projects`, `project_members`, `project_folders`, `project_user_prefs`) with `CREATE TABLE IF NOT EXISTS`, then idempotent MariaDB `ALTER TABLE … IF [NOT] EXISTS` statements that migrate older installs, and seeds the first admin. **Schema is duplicated** in `database.go` and `database/01_setup.sql`; change both. No migration framework: schema changes to existing tables are appended to `schema` as idempotent `ALTER … IF NOT EXISTS` (MariaDB-specific syntax).
- **Project access**: every project endpoint goes through `loadProject`, which uses `projectSelect` + `projectAccess` to allow only owner, `project_members` row, or admin role, and returns 404 otherwise. The query also yields `access` (`owner|member|admin`), `members` count and the current user's `starred`/`folder_id` (from `project_user_prefs`). It also returns `member_names` (GROUP_CONCAT); `listProjects` adds `editors` from `hub.ActiveEditors` (live WS presence, excluding the caller). Every `?` in `projectSelect` is the current user ID — always build args with `selectArgs(u, extra...)`. `projectOrder` puts starred projects first (most recently starred on top), then by `updated_at`.
- **Templates & cloning** (`templates.go`): `projects.is_template` (+ category/description) publishes a project to every user. `projectForCopy` widens `loadProject` with `OR p.is_template = 1` and marks such read-only rows `access: "template"`; only `listTemplates`, `cloneProject` and `thumbnail` may use it — every editing endpoint must keep using `loadProject`. Cloning flushes the hub under the source's `projectLock`, copies the working tree (no `.git`, hidden files or symlinks) and `git init`s a fresh repo. `projectTemplate()` is the default new-project content (us-letter, `es`/`mx`).
- **Personal organization** (`folders.go`): folders (with a `color` key from a fixed palette mirrored in `Projects.svelte` `FOLDER_COLORS`) and stars are per user and never affect other members; `updatePrefs` upserts `project_user_prefs` (keeps `starred_at` when re-starring) and only accepts the caller's own folders. Removing a member deletes their prefs row. Settings/delete/invite need `canManage` (owner or admin). New project endpoints must call `loadProject` rather than querying `projects` directly. Removing a member calls `hub.KickUser`, which sends `closed` with `revoked: true` and drops their sockets.
- `internal/httpapi` — `server.go`: routes (Go 1.22+ `"METHOD /path"` patterns), middleware, JSON helpers; `handlers.go`: auth/users; `projects.go`: projects, files, compile, git; `members.go`: members and user search; `ws.go`: collaboration WebSocket. `requireAuth` re-reads the user from the DB on every request (`currentUser(r)`). The WS endpoint can't use headers, so it authenticates with `?token=` itself and is registered outside `requireAuth`. Bodies capped at 1 MiB except `/upload` (20 MB). Long handlers (compile, git) extend the 15 s server write timeout with `longRequest`. Errors are `{"error": "<mensaje>"}`.
- `internal/collab` — the OT authority. `Hub` holds open `Doc`s keyed by `projectID:path`; text is `[]uint16` because CodeMirror positions are UTF-16 code units (`changeset.go` applies `ChangeSet.toJSON()` and builds diff changesets). A push is accepted only if its base version equals the current version; otherwise the client rebases (client-side, via `receiveUpdates`) and retries — the server never transforms. Bounded history (`maxHistory`) lets reconnecting clients resume with `since=`. Docs are saved to disk with debounce and unloaded when the last peer leaves (saved under the hub lock to avoid a stale reload race). **Disk can lag memory**: anything that reads project files (compile, git, raw download) must call `hub.FlushProject` first. External content changes to an open file (git pull, upload) must go through `hub.ReplaceText` so clients receive them as a normal update; deleted files go through `hub.CloseDoc`. `hub.BumpRev` increments a per-project revision and broadcasts `rev` — it's the cache key for compiled preview/PDF and the client's recompile trigger.
- `internal/typst` — runs `typst compile --root <projectDir> --diagnostic-format short`, outputs to `DATA_DIR/build/<id>/` (outside the repo so it's never committed); parses diagnostics.
- `internal/github` — minimal GitHub REST client (`/user`, `/user/repos`, branches); base URL from `GITHUB_API`. `httpapi/github.go` manages per-user `github_accounts` (token validated against `/user`, never returned) with a 2-minute in-memory repo-list cache. Projects reference an account via `github_account_id`; `projectSelect` joins it to get the token used by `s.remote(p)`. Only one's own accounts can be assigned (`ownAccount`), but a project keeps a foreign account when edited by an admin.
- `internal/gitsync` — shells out to git. HTTPS remotes only (`ValidateRemote`); token passed per call as `http.extraHeader`, never stored in `.git/config`; `core.symlinks=false` and disabled hooks so pulled content can't escape the project dir. Errors meant for users are `*gitsync.UserError` (`BranchMissing` marks an empty repo / missing branch: project creation then falls back to `git init` + template). Compile and git ops on a project are serialized by `Server.projectLock`.
- File paths from clients go through `cleanPath` (no traversal, no hidden/`.git` entries) and `regularFile` (Lstat, rejects symlinks).

### Frontend
- Hash routing in `App.svelte` (`#/`, `#/login`, `#/usuarios`, `#/p/<id>`); `Workspace.svelte` is lazy-loaded so CodeMirror stays out of the main bundle. Pages in `src/routes/`, shared code in `src/lib/`.
- `lib/editor/collab-client.js` — `CollabSession` owns one WebSocket + `EditorView` per open file: pushes `sendableUpdates` one batch at a time (`inflight` until `pushed`), applies `updates` with `receiveUpdates`, reconnects with backoff resuming from `getSyncedVersion`. Cursors are only sent when there are no pending local changes; received cursors are mapped through pending changes (`lib/editor/cursors.js`).
- `lib/editor/typst-lang.js` — hand-written `StreamLanguage` highlighter (markup/code/math mode stack); no Lezer grammar.
- `lib/workspace/Preview.svelte` (SVG pages as blob-URL `<img>`, recompiles on `refreshKey` changes driven by WS `rev` messages) and `PdfView.svelte` (fetches PDF blob into an iframe).
- `routes/Projects.svelte` — full-width page (rendered outside the `max-w-6xl` container in `App.svelte`); sticky sidebar on `lg`, scrollable chip row below it; grid/list layout and sort persisted in `localStorage`; polls the list every 30 s for live editors. Sidebar views (all/starred/own/shared/admin, folders, no folder), client-side search, optimistic star/move with rollback, HTML5 drag-and-drop onto sidebar folders. Cards use a stretched link (`after:absolute`) so the star/menu buttons can sit inside without nesting buttons in `<a>`.
- `lib/Thumb.svelte` — first-page PNG thumbnail with a module-level blob-URL cache (`invalidateThumb`). The Projects page has a `templates` view (gallery with category chips), clone/template modals and a "Desde plantilla" tab in the create modal.
- `lib/RepoPicker.svelte` — account select + searchable repo list + branch input (datalist), reused by the create-project modal and project settings. `routes/GitHubAccounts.svelte` manages accounts (`#/github`).
- `lib/session.svelte.js` — global session (`$state`), token in `localStorage` (`typsteditor.token`). `lib/api.js` — `request()`/`requestBlob()` wrappers; any 401 clears the session; `wsURL()` builds the editor socket URL.
- Page transitions stack views in one grid cell (`[grid-area:1/1]`). Tailwind CSS 4; design tokens and component classes (`btn-primary`, `btn-secondary`, `btn-sm`, `input`, `label`, `card`, `badge`, `alert-error`, `alert-info`) are in `src/app.css` — reuse them.
