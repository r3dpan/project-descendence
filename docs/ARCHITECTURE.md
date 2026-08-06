# Architecture

**Project:** Script automation platform (working name: TBD)
**Status:** see PLAN.md's "Current position" block — this file does not track
implementation progress, to avoid two places claiming to know it (§2 principle 2).
**Last updated:** 2026-08-05

---

## 1. What this is

A self-hosted platform for running scripts on demand or on a schedule. Scripts live
in git, execute inside containers spun up by Podman, and are triggered through an
HTTP API (and later a web UI). Logs and run history land in Postgres.

Conceptually similar to the commercial product ScriptRunner, but language-agnostic
and Linux-first.

**Primary goal:** a tool the author actually uses, built to learn Go, Postgres,
container orchestration and API design. Public adoption is explicitly *not* a goal.

### Prior art (checked, deliberately not used)

These already exist and do most of what this project plans. Worth knowing about, so
that "why not just use X" has an answer, and worth stealing ideas from:

| Project | License | Notes |
|---|---|---|
| [Windmill](https://www.windmill.dev/) | AGPL-3.0 | Closest match. Scripts → auto-generated UIs, cron, any Docker image as runtime, RBAC, Postgres, git sync. |
| [Rundeck](https://github.com/rundeck/rundeck) | Apache-2.0 | Job scheduler + runbook automation, self-service delegation, RBAC. JVM. |
| StackStorm | Apache-2.0 | Event-driven automation. |

The genuinely thin niche is PowerShell-centric self-service automation — the main
commercial options there (ScriptRunner, Devolutions PowerShell Universal, au2mator)
are all proprietary. That's not this project's target either, but it's the direction
with the most open space if that ever changes.

---

## 2. Design principles

These are the rules that decisions were made against. When a future decision is
unclear, check it against these.

1. **Delegate to native tooling.** If Podman, git or systemd already solves a
   problem, use theirs rather than writing our own. Fewer moving parts, better
   documented, someone else maintains it.
2. **Postgres is the single source of truth.** Anything derived (systemd units,
   built images, log files) must be regenerable from the database. Never have two
   places that both claim to know the truth.
3. **API-first.** Every capability is an HTTP endpoint. The CLI and the future web
   UI are both just clients. This also makes the platform callable from other
   automation, which for an automation platform is close to the whole point.
4. **Reproducibility via content addressing.** A run records the exact git commit
   SHA and the exact image digest it used. Any past run can be explained and repeated.
5. **Build the smallest thing that proves the design, then extend.** Vertical slices,
   not horizontal layers.

---

## 3. System overview

```
                              ┌──────────────┐
                              │   CLI (Go)   │   ← primary client for now
                              └──────┬───────┘
                                     │ HTTP + Bearer token
                                     ▼
 ┌──────────────────┐  writes  ┌────────────────────────────────────────────┐  reads   ┌──────────────────┐
 │  bare git repos  │◄─────────│  cmd/api  — HTTP server                    │─────────►│  run log files   │
 │  <GIT_REPO_DIR>/ │          │  · auth middleware (principal resolution)  │          │  <RUN_LOG_DIR>/  │
 │    <name>.git    │          │  · CRUD on jobs / runs / runtimes          │          │    <run_id>.log  │
 │                  │          │  · SSE log streaming + subscriber fan-out  │          │                  │
 │  job definitions │          │  · scans repos, commits uploaded files     │          │                  │
 │  and scripts     │          │  · serves embedded SPA (much later)        │          │                  │
 └────────▲─────────┘          └───────────────────┬────────────────────────┘          └────────▲─────────┘
          │                                        │ SQL + LISTEN                               │
          │                                        ▼                                            │
          │                               ┌─────────────────┐                                   │
          │                               │   PostgreSQL    │ ← queue, state, history,          │
          │                               └─────────────────┘   coordination, log index,        │
          │                                        ▲            notification bus                │
          │                                        │ SQL + NOTIFY                               │
          │                                        │ (api and supervisor never talk directly)   │
          │   reads one blob at    ┌───────────────┴────────────────────────┐                   │
          │   a run's pinned SHA   │  cmd/supervisor — single instance      │  writes           │
          └────────────────────────│  · claims queued runs                  │───────────────────┘
                                   │  · generates schedules' systemd units │  (sole writer)
                                   │  · container lifecycle                 │
                                   │  · copies a job's script in, then runs │
                                   │  · log capture (one attach per run)    │
                                   │  · crash reconciliation                │
                                   │  · log retention sweep                 │
                                   └───────┬────────────────────────────────┘
                                           │ REST over UDS
                                           ▼
                                   ┌───────────────┐
                                   │ Podman socket │
                                   │ (rootless)    │
                                   └───────┬───────┘
                                           │ creates
                                           ▼
                            ┌────────────────────────────────┐
                            │ ephemeral job containers       │
                            │ (python / powershell / bash…)  │
                            └────────────────────────────────┘
```

**Why api and supervisor are separate processes:** the API must stay stateless and
restartable without disturbing running jobs. Only one supervisor may run at a time
(it owns scheduling); the API can be restarted, crash, or eventually run multiple
copies without duplicating job execution.

---

## 4. Components

### 4.1 Postgres

Serves five roles:

- **State store** — jobs, runs, runtimes, principals, audit.
- **Work queue** — the supervisor claims runs with
  `SELECT ... FOR UPDATE SKIP LOCKED`, which lets exactly one worker grab a row
  without blocking others.
- **Coordination** — a Postgres *advisory lock* elects the single active scheduler
  if a second supervisor ever starts.
- **Log index** — sequence numbers and metadata; the log *bodies* go to files.
- **Notification bus** — `LISTEN`/`NOTIFY` carries "run 42 has more output" from the
  supervisor to the API, so live streaming needs no channel between the two
  processes (task 2.3, decision #19).

No Redis, no Celery, no separate broker. At this scale Postgres is more than enough
and it's one less thing to run.

### 4.2 Supervisor

The only component that touches Podman. Responsibilities:

- Poll for queued runs, claim them, execute them.
- Regenerate and reload the generated systemd `.timer`/`.service` unit pair
  for each `schedules` row (task 5.3, decision #27) — the supervisor is the
  sole writer of `~/.config/systemd/user/` for these units, mirroring how it
  is the sole component that touches Podman. Firing itself is systemd's job,
  not a supervisor loop's — schedules keep firing even while the supervisor
  is down. The api process only ever writes `schedules` rows; it never
  touches this directory.
- Attach to container output **once per run**, write it to that run's log file, and
  index it in Postgres. One attach no matter how many clients are watching. The
  followed attach is for liveness only; once the container exits its output is
  re-read and counted, and the capture redone from that read if the follow came up
  short (decision #21). A run's recorded output is complete or the supervisor says
  so in the log — never quietly partial.

  *Corrected at task 2.3.* This bullet used to say the supervisor also fans out to N
  subscribers. It doesn't, and can't: the subscribers are HTTP clients, and the
  supervisor serves no HTTP — api and supervisor never talk (§3). The supervisor
  instead emits a `NOTIFY` watermark ("run 42 has output through sequence 900"), and
  the **fan-out lives in the API** (`internal/logstream`), which holds one listening
  connection for the whole process and broadcasts to however many subscribers a run
  has. Slow consumers are dropped, never allowed to block: one listener goroutine
  serves every run, so a single frozen client would otherwise stall log delivery for
  everybody.
- On startup: list containers by their `run_id` label and reconcile against runs in
  a non-terminal state. Without this, every crash leaves orphaned containers.

### 4.3 Podman integration

- **Rootless**, under a dedicated service user. The Podman API "grants full access to
  all Podman functionality"
  ([docs](https://docs.podman.io/en/latest/markdown/podman-system-service.1.html)),
  so a rootful socket reachable from the app would make the app root-equivalent on
  the host — with user-supplied scripts as input.
- **Socket-activated** system service, so it starts on demand and stops when idle.
- **Log driver pinned to `k8s-file`, never inherited from the host** (decision #20).
  The host default is `journald`, which rate-limits and silently discards the
  overflow — measured at ~2500 lines lost from a 20000-line script, and a *whole*
  run's output lost when it started inside the same window. This is the one piece of
  container configuration the logs feature cannot survive being wrong.
- **Access method:** plain HTTP over the Unix domain socket, via Go's `net/http`
  with a custom `DialContext`.
  - Official Go bindings exist (`github.com/containers/podman/v5/pkg/bindings`,
    [README](https://github.com/containers/podman/tree/main/pkg/bindings)) and are
    first-class, but importing them pulls in the whole Podman module and needs
    native headers (gpgme, btrfs, devmapper) at build time. We use maybe 15
    endpoints; direct HTTP avoids that weight.
  - Reconsider the bindings if hand-rolling the client becomes a burden.
- **Every container is labelled** with `run_id`. This is what makes reconciliation
  possible.
- **Runs pin image digests, not tags.** Otherwise rebuilding an image silently
  changes what old schedules execute.

### 4.4 Runtimes (container images)

A *runtime* = curated base image + system packages + language packages.

The platform renders a templated Containerfile and tags the result with a hash of
those inputs, so identical definitions dedupe and reuse the build cache:

```dockerfile
FROM <curated-base>@sha256:...
ARG SYS_PKGS
RUN apk add --no-cache $SYS_PKGS      # or apt/dnf depending on base
COPY manifest /tmp/manifest
RUN <per-language install step>
```

Per-language declarative manifests use each ecosystem's own native format:

| Language | Manifest | Install step |
|---|---|---|
| Python | `requirements.txt` | `pip install -r` |
| PowerShell 7 | `.psd1` / JSON | `Install-PSResource -RequiredResourceFile` ([docs](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.psresourceget/install-psresource)) — supports version ranges and repositories; ships in PowerShell 7.4+ |
| Node | `package.json` | `npm ci` |

Order layers so package installation comes before anything that changes frequently,
or the build cache is useless.

Uploading a fully custom Containerfile is a *later* feature and uses the same build
path with the templating step skipped — not a parallel implementation.

**Operational note:** one-off builds accumulate layers. A retention/prune policy is
required, not optional.

### 4.5 Git

Scripts and job definitions live in bare git repositories on disk, under
`GIT_REPO_DIR`, accessed with the go-git library (decision #8) — no shelling out.

The important design consequence is not versioning but **content addressing**: a run
record stores `(repo, commit_sha, image_digest, params)`, which makes any historical
run fully explainable and repeatable.

**Who writes.** The **api is the sole writer** — it creates repositories, commits
files into them (task 3.7) and scans them (3.4). The **supervisor only reads**, and
narrowly: given a run's pinned commit SHA it reads one manifest and one script blob
(3.5). This is the mirror image of `RUN_LOG_DIR`, and like it, both processes must be
given the same path — a second shared directory, so decision #19's note about being
pinned to one host now applies twice.

Nothing needs a working tree. Reads walk the commit's tree; writes attach an
*in-memory* filesystem as the worktree over the on-disk object store, so a bare
repository stays bare and no checkout ever reaches disk.

**Sidecar manifest.** Each script has a `<name>.job.yaml` beside it, and that manifest
*is* the job (decision #23). Format v1 was specified whole and implemented in parts —
`runtime` (task 4.6), `params` (task 6.1) and `form` (task 7.8) were each rejected
with an error naming the phase until their turn came, and now all three are real:

```yaml
apiVersion: descendence/v1     # required; the format is versioned from the start
name: backup-db                # unique among live jobs; how the job is addressed
description: Nightly dump      # optional
script: backup-db.sh           # relative to the *manifest's own directory*
image: docker.io/library/alpine:3.20
# runtime: some-runtime-name                 # instead of image: exactly one of the two
# command: ["sh", "/run/job/backup-db.sh"]   # optional; default is the shebang
# timeoutSeconds: 1800                       # optional; platform default otherwise
params:                          # optional; task 6.1's contract, order is load-bearing
  - name: target_db
    type: string
    default: "prod"
form:                            # optional; task 7.8's layout metadata over params
  sections:                      #   above - never a second source of what a param is
    - title: Target
      fields:
        - target_db
```

Three rules worth stating, because each one is a decision rather than a detail:

- **`script:` resolves relative to the manifest, not the repository root.** That is
  what "sidecar" means: a directory holding a manifest and its script is a unit that
  can be moved, copied as the start of the next job, or vendored from elsewhere
  without rewriting paths inside it.
- **Invocation is the shebang.** The script is delivered at mode 0755 and argv is
  just its path, so the platform never needs a table of languages. `command:`
  overrides it.
- **An unknown key is an error, and a known-but-unimplemented key is a *different*
  error naming the phase.** Accepting `runtime:` and silently running something else
  is this system's characteristic failure mode — see the Phase 2 entries in HISTORY.

Consequences of the manifest living in git:

- Git is the source of truth for job definitions; the API/UI are editors for those
  files. The `jobs` table is a projection of them (decision #23).
- "Pull from an external repo" becomes the same code path as the local library —
  external repos simply bring their own manifests.
- Script bodies never need to be stored in Postgres.

**Delivery into the container.** A job's script is read from git and unpacked into
its container as a tar over `PUT /libpod/containers/{id}/archive`, between create and
start (decision #24).

### 4.6 Secrets

Use Podman's native secret mechanism
([docs](https://docs.podman.io/en/latest/markdown/podman-secret-create.1.html)).

- Prefer `type=mount` (secret appears as a file, default `/run/secrets/<name>`) over
  `type=env` — env vars leak into `podman inspect` and child process environments.
- Secrets are not committed by `podman commit` and not included in `podman export`.
- **The default `file` driver is unencrypted.** Acceptable for a single-user homelab;
  know that it is not a vault.
- Flat global namespace, ~500 KB cap → use a naming convention such as
  `<jobid>__<name>`.
- A `shell` driver exists that delegates store/lookup/delete to external scripts —
  that's the migration path to `pass` or `systemd-creds` later without app changes.

### 4.7 Parameters

Four separate concerns, deliberately not merged:

1. **Parameter contract** — declared in the sidecar manifest. Authoritative.
2. **Introspection** — best-effort auto-detection when creating a job. Never a
   runtime dependency, because quality varies wildly: PowerShell has a real AST
   parser; Python `argparse` effectively requires *executing* the script; Bash has
   nothing.
3. **Form definition** — widgets, labels, validation. (Later.)
4. **Binding map** — form field → parameter name. (Later.)

**Language-agnostic passing convention:** the platform writes parameters as JSON to a
known path (`/run/job/params.json`). Each runtime image ships a small shim that
translates that into a native invocation — splatting a hashtable for PowerShell,
`argparse` for Python, positional args for Bash. Adding a language is then "write a
20-line shim", and the core orchestrator stays language-neutral.

**Security:** container argv is always built as an **array**, never a shell string.
Form values interpolated into `sh -c` is the injection hole this class of tool is
famous for.

### 4.8 Scheduling

**Generated systemd (user) timers, not an in-process cron loop** (decision #27).
The `schedules` table in Postgres is authoritative; a `.timer`/`.service` unit
pair per schedule is a regenerable render target, the same relationship
`internal/runtimebuild` already has between a `runtimes` row and a
Containerfile — full regenerate-and-reload on change, never hand-edited.

The **supervisor** owns this render+reload, not the api process (§4.2) — the
existing advisory lock that already guarantees exactly one supervisor process
is what guarantees exactly one process ever touches
`~/.config/systemd/user/`. The api process's schedule CRUD is a plain
Postgres write; the supervisor's schedule-sync loop picks up the change on
its next poll tick. One consequence worth calling out: schedules keep firing
even while the supervisor is stopped, since systemd is host-level and
outlives the Go process — only *changes* to schedules lag until the
supervisor is running again to regenerate units.

`cron_expr` is standard 5-field cron syntax, translated to systemd's
`OnCalendar=` by `internal/scheduling.CronToOnCalendar` — deliberately scoped
to a conservative, explicitly-supported subset (single value, `*`, simple
`*/N` steps, comma-lists), rejecting anything else (ranges, combined
dom+dow) by name rather than risk a silent mistranslation. `robfig/cron/v3`
(the first non-Charm/non-pgx dependency since decision #17) validates
`cron_expr` at CRUD time and computes an informational, display-only
`next_due_at` — it never drives firing.

Missed windows (task 5.4) map onto the generated timer's `Persistent=`
directive, from a per-schedule `catch_up_policy` column: `skip` (default,
`Persistent=false`) or `catch_up` (`Persistent=true`, which fires **once** to
catch up, not once per missed occurrence). Timezone/DST (task 5.5) maps onto
the timer's `TimeZone=` directive from the schedule's `timezone` column —
not embedded in `OnCalendar=`.

Overlap policy (task 5.6) is a per-schedule `overlap_policy` column
(`skip`/`queue`/`concurrent`, default `skip`), enforced in the trigger
endpoint (`POST /api/v1/schedules/{id}/trigger`) rather than in the unit
itself. **`queue` and `concurrent` are behaviorally identical today**: the
supervisor's run-claim loop already executes runs strictly one at a time (a
known Phase 1/3 limitation, see PLAN.md's "Current position"), so a
"concurrent" schedule firing does not actually run concurrently with itself
— it queues a second row the same serial claim loop works through after the
first finishes. The distinction is preserved as stored data for when real
concurrency exists, not built as two code paths today.

Quadlet is the right tool for the *platform's own* services
([docs](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html));
note it requires cgroup v2. The schedule units here are plain systemd
`.timer`/`.service` units under `~/.config/systemd/user/`, not Quadlet units
— Quadlet's container-unit generation isn't the right fit here since a
schedule fires a CLI command (which creates a run through the normal API
path), not a container directly.

### 4.9 API

- Path-versioned: `/api/v1` from the first commit.
- **OpenAPI spec is the source of truth**, kept even though Swagger UI is deferred.
- **Runs are async.** `POST /api/v1/jobs/{id}/runs` returns `202 Accepted` with a run
  ID and `Location` header. Never block an HTTP request on a script finishing.
- **`Idempotency-Key` header** on run creation, deduplicated server-side. A retrying
  pipeline that times out mid-request must not double-execute a job.
- **Cursor (keyset) pagination** on runs — that table grows forever and offset
  pagination both degrades and skips rows under concurrent inserts.
- **Errors:** RFC 9457 `application/problem+json`.

### 4.10 Authentication

Two credential types, resolved by one middleware (`RequireAuth`,
`internal/api/auth.go`) into a common `principal`:

| Caller | Credential | Storage |
|---|---|---|
| Machines / CLI | Opaque token, prefixed (`sra_live_<random>`) | Only a hash in `principals.token_hash`; plaintext shown once |
| Browsers | Server-set session cookie, from a local-account password login (task 7.3) | `HttpOnly`, `Secure`, `SameSite=Lax`; only a hash in `sessions.token_hash` |

Built as password auth ahead of OIDC, per this section's original note that a local
account is fine first (§7 still defers OIDC/Authentik). A
`kind='user'` principal carries a bcrypt `password_hash` (migration
`00008_web_auth.sql`) — a symmetric CHECK to `token_hash`'s: a `user` row must
have one, a `token` row must not. `POST /api/v1/auth/login` checks it and mints a
row in a `sessions` table (principal_id, hash-only `token_hash`, `expires_at`);
`POST /api/v1/auth/logout` deletes the row outright, not a soft-delete — a dead
session has no history worth keeping, unlike a run or a job. `cmd/seed -kind user`
mints the first local account the same way the bootstrap token is minted: a
generated credential printed once. When OIDC does arrive, it plugs into the same
`sessions` table and the same `RequireAuth` cookie path; only how the cookie gets
issued changes.

**Never store JWTs (or this session token) in `localStorage`.** Any XSS becomes
total account compromise and they can't be revoked.

**Authorization (Phase 8): real roles and permissions, not the earlier flat
`scopes` array.** Every principal — token or user — has exactly one role
(`principal_roles`, `principal_id` UNIQUE), chosen from three fixed built-ins
seeded by migration `00009_rbac.sql`:

| Role | Can do |
|---|---|
| `viewer` | Read everything (`jobs:read`, `runs:read`, `schedules:read`, `runtimes:read`, `repos:read`, `users:read`) |
| `operator` | Everything `viewer` can, plus `runs:trigger`, `runs:cancel`, `schedules:trigger` — run and watch, cannot create/edit jobs, schedules, runtimes, repos, or manage users |
| `admin` | Every permission, including `users:read`/`users:write` |

Fourteen `resource:verb` permission keys total (`jobs:{read,write}`,
`runs:{read,trigger,cancel}`, `schedules:{read,write,trigger}`,
`runtimes:{read,write}`, `repos:{read,write}`, `users:{read,write}`) — one
`:read`/`:write` split per resource, not finer, matching what handlers
already branch on. `RequireAuth` resolves a principal's effective permission
set in the same request as the principal itself (`GetPrincipalPermissions`,
one indexed join), and `RequirePermission("<key>", handler)` composes after
it in the route table (`cmd/api/main.go`) — authorization stays out of
handler bodies entirely, the same way `RequireAuth` itself already is.
Self password-change (`PATCH /api/v1/users/me/password`) is the one
exception: gated by "acting on self", not a permission key, since it isn't
something a role should be able to grant onto *other* principals.

Users and tokens are both manageable through the API/CLI/web UI now
(`GET/POST /api/v1/users`, `/api/v1/tokens`, read-only `/api/v1/roles`;
`descendence user|token|role ...`; the web UI's Users/Tokens/Settings
pages) — closing the gap where `cmd/seed` was the *only* way to create a
principal. `cmd/seed` still is the bootstrap escape hatch for the very
first admin (`-role`, default `"admin"`): creating a user via the API
requires `users:write`, which nothing has yet on a fresh database, so
`cmd/seed` assigns a role by a direct DB write, bypassing `RequirePermission`
by construction. Removing a principal's access is always a soft-revoke
(`revoked_at`, already the filter every principal-lookup query applies) —
never a hard delete, since `runs.principal_id` is `ON DELETE RESTRICT` and a
principal with run history can't be deleted anyway; soft-revoke makes the
no-history case behave the same as the has-history case instead of one
silently succeeding and the other 500ing on a constraint violation.

### 4.11 Web UI (Phase 7, in progress)

Decision: a JavaScript SPA consuming the same API, **served from the same origin** as
the API (built with Vite, embedded into the Go binary with `//go:embed`).

Implemented (task 7.1–7.5): React + TypeScript under `web/`, scaffolded with
`create-vite`. Types are generated from `api/openapi.yaml` via
`openapi-typescript` (`npm run gen:api` → `web/src/api/schema.ts`); request
*logic* is hand-written (`web/src/api/client.ts`), mirroring
`internal/client`'s `do()`/`send()`/`requestOptions` shape rather than being
fully codegen'd — splits decision #11 (spec as contract) from decision #15
(hand-written logic) instead of picking one over the other for the browser
client. `web/embed.go` (package `webdist`) holds the `//go:embed dist`
directive; since embed needs the directory to exist at compile time,
`web/dist/index.html` is a checked-in placeholder and `web/.gitignore`
excludes the rest of `dist/`, which a real `npm run build` fills in locally.
`cmd/api/main.go` mounts the embedded build as a catch-all at `/`, behind
every `/api/v1/*`, `/healthz` and the exact-match root route — Go 1.22's mux
always prefers the more specific pattern, so `/` keeps returning JSON server
info for machine clients and only the SPA's own client-side routes hit the
catch-all, with an `index.html` fallback for any path with no matching
static file (so a refresh on `/runs/42` doesn't 404).

The same-origin choice is not cosmetic. **`EventSource` cannot set custom headers** —
it issues GET only, with no way to attach `Authorization`
([whatwg/html#2177](https://github.com/whatwg/html/issues/2177)). A bearer-token SPA
on a different origin therefore cannot use the native SSE API for live logs; the
workarounds are tokens in query strings (logged, cached, in browser history), a
polyfill, or hand-rolling `fetch` + `ReadableStream` with a custom SSE parser.
Same-origin + cookie session means `new EventSource('/api/v1/runs/42/logs')` simply
works. External clients still use bearer tokens against the same endpoints.
**Confirmed live** (task 7.5): a real run's log stream, both `log` and `state`
events, arrived over a plain `new EventSource(...)` with no query-string token
and no polyfill, authenticated purely by the cookie `RequireAuth` set at login.

Server-rendered HTML + htmx was considered and rejected: the form builder is a
genuine client-side application, and a single API with one client type is simpler
than two rendering strategies.

---

## 5. Data model sketch

Phases 0–4's tables (`principals`, `repos`, `jobs`, `runs`, `run_logs`, `runtimes`)
are built; this sketch of them is illustrative, not authoritative — the migrations in
`migrations/` are the source of truth for exact columns and constraints.
`audit` is still a sketch, not yet built.

```
principals    id, kind(user|token), name, token_hash, password_hash,
              created_at, expires_at, revoked_at
              -- a token principal carries token_hash; a user principal
              -- (task 7.3) carries password_hash instead - symmetric CHECKs
              -- tie each column to its kind, mirroring one another.
              -- scopes[] (Phase 0-7) was dropped by migration 00009_rbac.sql
              -- in favour of principal_roles below.
roles         id, name(admin|operator|viewer), description, created_at
              -- Phase 8. Three fixed built-ins, seeded by migration; never
              -- admin-editable (ARCHITECTURE.md §6 decision #30).
permissions   id, key(e.g. "jobs:write"), description
              -- Phase 8. Fourteen resource:verb keys, seeded by migration.
role_permissions   role_id, permission_id
              -- Phase 8. Which permissions a role grants.
principal_roles    principal_id(UNIQUE), role_id
              -- Phase 8. Exactly one role per principal - the UNIQUE, not
              -- just the composite PK, is what enforces that. role_id is
              -- ON DELETE RESTRICT: the three built-ins are never deleted
              -- by application code.
sessions      id, principal_id, token_hash, created_at, expires_at
              -- browser sessions (task 7.3). Hash-only storage, same
              -- reasoning as principals.token_hash. Logout deletes the row
              -- outright - no soft-delete, a dead session has no history.
repos         id, name, path, kind(local|external), remote_url, default_branch,
              last_synced_at, last_synced_commit_sha
jobs          id, repo_id, manifest_path, name, runtime_id, enabled,
              description, script_path, command[], image_ref, timeout_seconds,
              synced_commit_sha, synced_at, deleted_at
              -- a projection of the manifests a scan found (decision #23).
              -- `enabled` is the only column git does not own; everything
              -- else is rewritten by the next sync. deleted_at marks a
              -- manifest that has gone, keeping past runs explainable.
runtimes      id, name, base_image, sys_packages, lang, lang_manifest, input_hash,
              image_digest, build_status(pending|building|ready|failed),
              build_error, built_at, image_pruned_at, created_at
              -- unlike jobs, not a git projection: created and rebuilt
              -- directly through the API (task 4.5). input_hash doubles as
              -- the local image tag, so identical definitions dedupe.
              -- image_pruned_at mirrors runs.logs_pruned_at's pattern - the
              -- row survives a prune, only the image bytes go.
runs          id, job_id, schedule_id, principal_id, state, idempotency_key,
              commit_sha, runtime_id, image_digest, params_json,
              container_id, exit_code,
              queued_at, started_at, finished_at
              -- runtime_id/image_digest are set only when the job named a
              -- runtime rather than an image; resolved once at creation and
              -- never re-resolved, so rebuilding the runtime afterwards
              -- cannot change what an already-created run executes.
              -- schedule_id is set only for runs the schedule trigger
              -- endpoint created (task 5.6); ON DELETE SET NULL, same
              -- reasoning as job_id - deleting a schedule must not sever a
              -- past run's explainability.
run_logs      run_id, seq, stream(stdout|stderr), ts, byte_offset, byte_length
              -- index only, per decision #18/§4.1: log bodies live in files,
              -- not in Postgres. byte_offset/byte_length point into the file;
              -- there has never been a `text` column here.
schedules     id, job_id, cron_expr, timezone, catch_up_policy, overlap_policy,
              enabled, created_at, updated_at
              -- fleshed out at task 5.2. No next_due_at: nothing computes or
              -- reads it as stored state under generated systemd timers
              -- (decision #27) - it would be a second, competing source of
              -- truth for "when does this fire" alongside systemd itself.
              -- Display-only next-fire estimates are computed on the fly via
              -- robfig/cron, never stored.
audit         id, principal_id, action, target, ts, detail_json
```

**Run states:** `queued` → `running` → one of `succeeded` / `failed` / `cancelled` /
`lost`.

`lost` matters and is easy to forget: a run whose container vanished is not the same
as one that failed, and the reconciler needs somewhere to put it.

---

## 6. Decision log

Recording *why*, because in three months the reasoning will be gone.

| # | Decision | Rationale | Revisit if |
|---|---|---|---|
| 1 | Build it despite Windmill/Rundeck existing | Personal tool + learning project; adoption not a goal | Never — but steal their ideas |
| 2 | Go, not Python | Long-lived daemon (compiler catches scheduler bugs), streaming/concurrency fits goroutines, single static binary, first-class Podman ecosystem. Performance was *not* the reason — the workload is I/O-bound | Momentum stalls badly |
| 3 | Podman REST over UDS, not Go bindings | Bindings pull the whole Podman module + native build deps for ~15 endpoints | Client maintenance becomes painful |
| 4 | Rootless Podman | API grants full Podman access; rootful + user scripts = root-equivalent | Never |
| 5 | Postgres as queue and lock | Already required; avoids Redis/broker | Scale far beyond a homelab |
| 6 | api and supervisor as separate processes | API restartable without disturbing jobs; exactly one scheduler | Never |
| 7 | Sidecar manifest in git | Git as source of truth; unifies local and external repos | Never |
| 8 | Use git via go-git library | Need fine-grained in-process control; no subprocess, no git binary to depend on | Bare-repo operations outgrow what go-git does well |
| 9 | Podman native secrets | Native tooling; `shell` driver is the upgrade path | Need real encryption at rest sooner |
| 10 | JSON params file + per-runtime shim | Keeps core language-agnostic | Never |
| 11 | Handwritten OpenAPI spec as contract | Handwritten handlers; free CLI client | Never |
| 12 | SPA over server-rendered HTML | Form builder is genuinely client-side; API is reusable externally | Never — but note beauty is CSS, not framework |
| 13 | SPA same-origin, cookie session | `EventSource` can't set `Authorization` headers | Never |
| 14 | Swagger UI deferred, spec kept | UI is a static asset behind a flag; spec is codegen input | Whenever convenient |
| 15 | Hand-write routing and handlers; no chi, no oapi-codegen | Learning value is the point of the project; stdlib `net/http` + Go 1.22 patterns are sufficient. Reverses the codegen half of #11 — the spec remains the contract | Handler boilerplate becomes genuinely unmanageable |
| 16 | goose for migrations, not golang-migrate | Go-based migrations available for the Phase-1 bootstrap token (crypto/rand + SHA-256, not expressible in SQL); no dirty-flag state to force-clear after a failed migration; Postgres-only project makes golang-migrate's driver breadth irrelevant | Migrations ever need running by something that isn't Go |
| 17 | CLI built on the Charm stack (`bubbletea`, `bubbles`, `lipgloss`), not plain `fmt.Println` | A run is a *live* thing — queued → running → terminal, on the order of seconds to an hour. Watching that is genuinely interactive, and a good TUI is the difference between a tool that gets used and one that doesn't. Deliberately narrower than #15's "hand-write it": rendering is not where this project's learning value is, and reimplementing a terminal renderer would be busywork, not education. Command dispatch and flag parsing stay stdlib (`flag`) — no cobra | The TUI outgrows bubbletea, or the CLI stops being the primary client |
| 18 | **Log retention: run records forever, run *output* for 30 days.** Time-based, swept hourly by the supervisor, both halves (index rows and files) deleted together | The question an operator actually asks is "can I still see what last month's backup printed" — a question about time. Count-based answers it differently depending on how busy the month was; size-based lets one chatty job evict everyone else's history. The run row is the audit trail (what ran, when, under whose token, how it ended): small, structured, worth keeping forever. The output is the bulky part and the part whose value decays. The sweep lives in the supervisor because the advisory lock (#16 era, task 1.16) already guarantees exactly one of it. `runs.logs_pruned_at` exists so the API can tell "printed nothing" from "output deleted" — without it both look like an empty log, and the second silently lies | Disk pressure arrives before 30 days, i.e. a per-run size cap becomes necessary — deliberately not built, since it has not happened |
| 19 | **Live logs travel by `LISTEN`/`NOTIFY`, and the API reads log files directly** (a shared directory the supervisor alone writes). Payloads are watermarks — "run 42 has output through seq 900" — never log text | The API serves SSE but the supervisor holds the output, and the two never talk (§3), so something has to bridge them. Postgres is already the coordination layer (#5), needs no new port or service discovery, and keeps the API stateless. Watermarks rather than payloads mean a dropped or missed notification costs latency, not correctness — so slow subscribers can be dropped freely and a listener reconnect needs no replay protocol; a slow safety-net poll covers the gap. This does add a third channel the §3 diagram originally lacked: a filesystem shared by both processes, which is what pins them to one host | Multi-node execution arrives (already deferred, §7) — the shared directory is the assumption that breaks first, and object storage or streaming bodies through Postgres would be the replacements |
| 20 | **Every container is created with the `k8s-file` log driver, explicitly — never the host default** | The host default here is `journald`, which rate-limits: 10000 messages per 30 seconds as shipped, after which it discards the remainder *and the rest of the window*. Measured, not theorised — a 20000-line script reliably lost ~2500 lines, a second run started inside the same window lost **all** of its output, and the follow stream then never terminated, leaking the capture goroutine. Nothing reported an error at any layer, because from journald's point of view nothing went wrong. No care further down the pipeline can recover a line the log driver dropped, so this one line of container config is load-bearing for the entire logs feature. `k8s-file` writes a per-container file with no limiter, which podman deletes with the container — after the supervisor has taken its own durable copy | The host default stops being journald *and* the replacement is known-lossless; or a run's output grows large enough that an unbounded per-container file is a disk risk, at which point `max-size` is the knob |
| 21 | **A run's output is captured twice: followed live, then re-read authoritatively once the container has exited.** The second read is counted, not written, and only replaces the first if the first came up short | Following is the only way to see output while a run is happening, and a followed stream cannot be trusted to be complete: libpod stops the follower the moment the container exits, without draining what the container had already written. Measured repeatedly at 2600-7100 lines missing from a 20000-line script, worse under load, and the stream ends *cleanly* every time — there is no error to notice, only a shorter log than the run produced. So the follow provides latency and the post-exit read provides truth. Recapturing is a full redo rather than a merge, which is only safe because libpod replays the same bytes in the same order: the same lines get the same sequence numbers and the same offsets, exactly the property the reconciler's adoption path already depends on. Only the capture timestamp changes. The check itself is a line count, so a complete capture — the normal case — pays one extra read and no writes | libpod gains a way to follow a stream to a guaranteed end, or the second read stops being cheap relative to the run |
| 22 | **The CLI is both a set of commands and an interactive application.** Bare `descendence` on a terminal opens a navigable app (menu → runs → detail → live logs, plus a new-run form); every flag command keeps working unchanged, and bare `descendence` *without* a terminal still prints usage and exits 2 | Two different jobs, and neither substitutes for the other. Exploring — what ran, what did it print, stop that one — is navigation, and doing it through one-shot commands means retyping run ids and re-deriving context every time. Automation is the opposite: it needs exit codes that propagate, output that pipes, and `-detach` that composes, which §2 principle 3 makes a goal rather than a nicety. So the app is an addition, not a replacement. The TTY guard is what keeps them from colliding: a script that runs `descendence` to check the install must never find itself talking to a full-screen app it cannot answer. Narrower than it looks next to #17 — dispatch and flag parsing are still stdlib `flag`, still no cobra; this adds an entry point, not a framework | The app and the commands start disagreeing about what an operation means, or the app grows past what one person maintains by hand |
| 23 | **A job is a script's *interface*, authored in git; the `jobs` table is a projection of it.** Git holds identity, description, script path, image, invocation and (later) the parameter contract and form layout. Postgres holds a pointer plus `enabled`, schedules and run history | The test is "would this field still be true if someone else cloned the repo into their own installation?" — yes means git, no means Postgres. A job is everything that is only correct *relative to a particular version of the script*: change what a script accepts and its parameter contract, form and invocation must all change in the same commit or they are lying about it. Git can express "these facts were true together at `abc123`"; Postgres cannot. The thin reading — a job as "this script plus that runtime" — was rejected precisely because it would be a two-field join row with no authored content, where versioning is ceremony. What makes it fat is the manifest being the form builder's output (§7.8 calls that the largest single piece of the project). This narrows §2 principle 2 for one table, so it is written down rather than left as drift: the projection is *regenerable by re-scanning*, which is the same status principle 2 already grants systemd units and built images. Consequences: `enabled` is the only column a sync must never write, or pausing a job becomes something the next scan undoes; a vanished manifest soft-deletes, because `runs.job_id` is ON DELETE SET NULL and a hard delete would sever every past run from what it ran; and "same script, three databases" is one job with a parameter, not three jobs | Job definitions ever need to be edited by something that cannot commit — at which point the API grows a manifest *renderer*, and the cost is that it cannot round-trip a hand-written file's comments or unknown keys |
| 24 | **A job's script is delivered into its container as a tar over `PUT /libpod/containers/{id}/archive`**, between create and start — not bind-mounted from the host | A bare repository has no working tree, so a bind mount would first need the blob materialised into a per-run host directory: created before create, removed after the container is gone, and swept by the reconciler when the supervisor is SIGKILLed mid-run — which Phase 1e proved happens. The tar path has no on-disk state to leak at all: blob → `archive/tar` in memory → HTTP. It also hands podman no host path, which matters because a mount source is resolved in *podman's* namespace rather than the supervisor's — identical today, and not identical if the supervisor is ever a Quadlet container or the socket becomes remote. Finally, a tar header states uid/gid/mode outright, where a bind mount inherits host ownership squashed through the user namespace and breaks for any image that does not run as root. Cost was a raw-body request path in the podman client, which until now JSON-encoded every body unconditionally. Delivery is *before* start because the container filesystem exists from creation, and doing it after would race the entrypoint against the file it is meant to execute | Runs need more than a couple of small files in the container, or something must be shared *back* out of it |
| 25 | **Runtime base images are Debian, not Alpine** | PowerShell 7 ships official images built on Debian; PSResourceGet and the modules it installs are tested against glibc, and musl compatibility on Alpine is a known source of silent breakage for .NET-based runtimes. Python wheels also resolve more reliably against glibc — Alpine's musl forces source builds for packages that ship manylinux (glibc) wheels, which is slower and sometimes fails outright for packages with native extensions. Node was the only one of the three languages (§4.4) that would have preferred Alpine's size. One base family across all three languages was chosen over mixing, so the Containerfile template (§4.4) does not need a per-language base-image branch | Image size becomes a real constraint (homelab disk pressure, slow pulls) and outweighs the compatibility cost |
| 26 | **Every PowerShell runtime's Containerfile sets `ENV DOTNET_SYSTEM_NET_DISABLEIPV6=1`** | Measured, not theorised, at task 4.6's exit check: on a network where IPv6 is routed but blackholed rather than rejected outright, `curl` and Python's `urllib` fall back to IPv4 in well under a second (Happy Eyeballs), but .NET's `HttpClient` — what `Install-PSResource`/`PowerShellGet` use to reach PSGallery — does not fall back nearly as fast, and hung past its own 100s internal timeout on every attempt, retries included, in this platform's development sandbox. A plain HTTPS `HEAD` request that hung past 100s completed in 0.8s with this one variable set, and nothing else tried (retry loops, `--network=host`, an older `Install-Module` code path) fixed it. Scoped to the PowerShell install step only — it is a .NET-specific workaround and irrelevant to `pip`/`npm`, which were unaffected | The host network's IPv6 path is fixed, or the target deployment host doesn't blackhole IPv6 in the same way and the variable is confirmed unnecessary there — safe to leave set either way, since it is a no-op where IPv6 works |
| 27 | **Scheduling (Phase 5) uses generated systemd (user) `.timer`/`.service` units, not an in-process cron loop, and the supervisor — not the api process — owns rendering and reloading them.** `robfig/cron/v3` is added as a dependency for cron validation and display purposes only, never for firing | Postgres (`schedules`) stays authoritative either way; systemd units are a regenerable render target, the same relationship `internal/runtimebuild` already has between a `runtimes` row and a Containerfile — decision #23's "regenerable projection" pattern, applied a third time. Firing survives a supervisor crash or restart for free, since systemd is host-level and outlives the Go process — closing Phase 5's exit check ("across a supervisor restart") more directly than an in-process timer ever could, and `Persistent=true` gives missed-window catch-up (task 5.4) as a systemd primitive instead of app code (semantics: fires **once** to catch up, never once per missed window). `TimeZone=` on the generated timer gives timezone/DST handling (task 5.5) to systemd's own well-tested calendar evaluator rather than reimplementing it. The supervisor, not the api process, owns the unit files because the api process's only host side effects until now were Postgres writes, git repo writes and log reads — adding `systemctl --user` there would have been a new trust boundary; the supervisor already is "the only component that touches Podman" (§4.2), and its existing advisory lock (one supervisor process, guaranteed) is exactly the guarantee needed for "exactly one process touches `~/.config/systemd/user/`" too, with no new locking primitive. This makes schedule CRUD a plain Postgres write from the api's point of view, same shape as jobs/runtimes CRUD, with the supervisor's schedule-sync loop (task 5.3) picking up the change asynchronously on its next poll tick — a real, if small, propagation-delay trade-off, accepted at homelab scale. `cron_expr` → systemd `OnCalendar=` translation (`internal/scheduling.CronToOnCalendar`) is deliberately scoped to a conservative subset (single value, `*`, simple `*/N` steps, comma-lists) and rejects anything else (ranges, combined dom+dow) by name, matching this codebase's "unknown key is an error, not silently wrong" posture (`internal/manifest.Parse`) rather than risk a subtly wrong translation that fires at the wrong time silently — exactly the failure shape decisions #20/#21/#26 all found the hard way. Overlap policy (task 5.6, per-schedule `skip`/`queue`/`concurrent`) is enforced in the trigger endpoint, not the unit; **`queue` and `concurrent` are behaviorally identical today** because the supervisor's run-claim loop already executes runs strictly one at a time (the Phase 1/3 concurrency limitation flagged in PLAN.md) — worth stating plainly rather than implying "concurrent" does something it can't yet | Real run concurrency arrives (at which point `queue` and `concurrent` need to actually diverge), or the propagation delay between a schedule CRUD write and the supervisor regenerating its unit proves too slow in practice |
| 28 | **PowerShell AST introspection (task 6.7) is usable for a future best-effort form-builder suggestion, never as a source of truth.** `[System.Management.Automation.Language.Parser]::ParseFile` cleanly extracts a `param()` block's names, static types and `[ValidateSet(...)]` values | Prototyped live against `mcr.microsoft.com/powershell:7.4-debian-12` (no bare `pwsh` in this dev environment, matching decision #25's reasoning for why that image exists at all) with a five-parameter sample script and a deliberately-broken one. Two real gotchas, not hypothetical ones: (1) a `[Parameter(...)]` attribute's `Mandatory` named argument is an *expression AST*, not a value — checking only whether the argument is *present* silently treats `Mandatory = $false` as mandatory, caught only by testing a script that actually sets it false; correct detection means reading `NamedArgumentAst.ExpressionOmitted` (the bare `-Mandatory` shorthand) or comparing `Argument.Extent.Text` against `'$true'`. (2) `DefaultValue` is the default's raw *source text*, not a value — turning `"default-tag"` into this platform's manifest default string is fine, but a script with `$Tag = (Get-Date)` or any non-literal expression has no default this system could produce without executing script code, which introspection must never do (ARCHITECTURE.md's whole "best-effort, never a runtime dependency" framing exists for exactly this). Type mapping onto the platform's four-value contract (§4.7) is lossy in both directions: `SwitchParameter` behaves as `$false` by default but carries no `DefaultValue` node to read that from, and anything outside string/numeric/bool/switch (arrays, hashtables, custom types) has no destination in the contract and must be skipped, not guessed at. On the robustness side: a script with no `param()` block parses cleanly to an empty result, and a syntactically broken one fails fast with `ParseFile`'s own error list and a non-zero exit — neither hangs nor crashes the process, so a caller can treat "introspection didn't work for this script" as an ordinary, non-fatal outcome rather than something requiring special handling | Phase 7's form builder actually gets built and wants a "guess the fields for me" affordance — implement then, reusing this prototype's mandatory/default-extraction fixes, and keep it advisory: the manifest's own `params:` contract (task 6.1) stays authoritative, and a suggestion this produces is something an author reviews and commits, never something the platform trusts unread |
| 29 | **Phase 7's browser auth is a local username+password principal against a new `sessions` table, not OIDC** | ARCHITECTURE.md §4.10 always allowed "a login form against a local account is fine first," and §7 keeps deferring OIDC/Authentik past this phase regardless of which comes first - so building password auth now does not foreclose OIDC later, it just means `RequireAuth`'s cookie path and the `sessions` table already exist for OIDC to plug into rather than being built alongside it. `password_hash` on `principals` mirrors `token_hash`'s pattern exactly (symmetric kind-tied CHECKs, hash-only storage), so a `user` principal and a `token` principal stay visibly the same *kind* of thing rather than diverging in shape. Migration 00001's original comment ("kind='user' rows are placeholders for OIDC-backed browser sessions") is superseded by this, not merely stale - fixed in the same change (`00008_web_auth.sql`) per CLAUDE.md's doc rule | OIDC/Authentik integration (§7) actually gets scheduled, at which point it targets the same `sessions` table and `RequireAuth` cookie path already built here - only how the cookie gets issued changes, not how it is checked |
| 30 | **RBAC (Phase 8) is real `roles`/`permissions`/`role_permissions`/`principal_roles` tables, not just enforcing the existing `scopes` array — but fixed built-in roles (`admin`/`operator`/`viewer`), not an admin-editable custom-role builder, exactly one role per principal, and global (not per-resource-instance) permissions** | `scopes text[]` existed from Phase 0 but was checked in exactly one handler (`TriggerScheduleHandler`'s `principalHasScope`) — everywhere else was "any authenticated principal = full access," which stopped being defensible once user/token *management* also needed building (admin-only mutation needs something real to gate on). A custom-role builder was rejected because at single-operator homelab scale nobody is composing bespoke permission sets — three roles cover every real distinction this deployment has ("can read", "can also run things", "can also manage access") — and it would need a role-editor UI/CLI/API surface with no one asking for it. One role per principal (enforced by `principal_roles.principal_id` being `UNIQUE`, not just the composite PK) for the same reason: multi-role assignment is a lightweight reintroduction of the custom-permission-set idea just rejected. Per-resource-instance scoping (e.g. "can only manage job 12") was rejected as a materially bigger data model and enforcement surface for a distinction nothing here needs. `RequirePermission` composes after `RequireAuth` in the route table rather than inline checks (`principalHasScope`'s pattern) so authorization never touches a handler body — inline checks don't scale past the one call site they started at. Existing `scopes` data was backfilled onto the nearest-equivalent role (`admin` in scopes → `admin`; `run` → `operator`; else → `viewer`) in the same migration that drops the column — a clean cutover, not a compatibility shim, since this is a single-operator deployment with no fleet of principals to migrate gradually. `cmd/seed` remains the bootstrap escape hatch (`-role`, replacing `-scopes`): the very first admin can't be created through an API that requires `users:write` to create anyone | A second installation profile genuinely needs custom permission sets or multi-role assignment — at homelab scale this has not come up and is not expected to |

---

## 7. Deliberately deferred

Not forgotten — sequenced. See PLAN.md for when.

- Web UI / SPA — in progress: read-only slice (7.1–7.5) built; trigger runs,
  job/runtime management (7.6–7.7) still deferred
- Form builder (7.8)
- OIDC / Authentik integration
- External git repo sync + webhooks
- Custom Containerfile upload
- Swagger UI
- Multi-node execution

---

## 8. Open questions

Unresolved. Resolve at the phase indicated.

| Question | Resolve at |
|---|---|
| ~~In-process cron vs. generated systemd timers~~ | **Resolved at task 5.1 — decision #27: generated systemd (user) timers, owned by the supervisor** |
| ~~Log retention: how long, and prune where (files vs Postgres)?~~ | **Resolved at task 2.2 — decision #18** |
| ~~Image prune policy — time-based, count-based, or reference-based?~~ | **Resolved at task 4.7 — manual, API-triggered: explicit ids or an age threshold over unreferenced images, invoked either directly or via the same sweep cadence as decision #18's log retention** |
| ~~Does PowerShell's AST parser give usable parameter introspection? Prototype needed~~ | **Resolved at task 6.7 — decision #28: yes, usable as a best-effort form-builder suggestion at Phase 7, never as a source of truth** |
| ~~Base image family — Alpine (small) vs Debian (compatible, esp. for PowerShell)?~~ | **Resolved at task 4.2 — decision #25** |
