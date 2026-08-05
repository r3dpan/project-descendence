# Architecture

**Project:** Script automation platform (working name: TBD)
**Status:** Design agreed, not yet implemented
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
   ┌────────────────────────────────────────────┐        ┌──────────────────┐
   │  cmd/api  — HTTP server                    │ reads  │  run log files   │
   │  · auth middleware (principal resolution)  │───────►│  <RUN_LOG_DIR>/  │
   │  · CRUD on jobs / runs / runtimes          │        │     <run_id>.log │
   │  · SSE log streaming + subscriber fan-out  │        └────────▲─────────┘
   │  · serves embedded SPA (much later)        │                 │
   └───────────────────┬────────────────────────┘                 │
                       │ SQL + LISTEN                             │
                       ▼                                          │
              ┌─────────────────┐                                 │
              │   PostgreSQL    │  ← queue, state, history,       │
              └─────────────────┘    coordination, log index,     │
                       ▲             notification bus             │
                       │ SQL + NOTIFY                             │
                       │ (api and supervisor never talk directly) │
   ┌───────────────────┴────────────────────────┐                 │
   │  cmd/supervisor — single instance          │  writes         │
   │  · claims queued runs                      │─────────────────┘
   │  · scheduler (cron)                        │  (sole writer)
   │  · container lifecycle                     │
   │  · log capture (one attach per run)        │
   │  · crash reconciliation                    │
   │  · log retention sweep                     │
   └───────┬─────────────────────┬──────────────┘
           │ REST over UDS       │ shell out
           ▼                     ▼
   ┌───────────────┐      ┌─────────────┐
   │ Podman socket │      │ git (bare   │
   │ (rootless)    │      │ repos)      │
   └───────┬───────┘      └─────────────┘
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
- Evaluate schedules and enqueue runs when due.
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

Scripts live in bare git repositories on disk. We **shell out to `git`** rather than
using an in-process implementation — principle 1.

The important design consequence is not versioning but **content addressing**: a run
record stores `(repo, commit_sha, image_digest, params)`, which makes any historical
run fully explainable and repeatable.

**Sidecar manifest.** Each script has a `<name>.job.yaml` beside it in the repo,
declaring runtime, packages, parameter contract and (later) form layout. Consequences:

- Git is the source of truth for job definitions; the API/UI are editors for those files.
- "Pull from an external repo" becomes the same code path as the local library —
  external repos simply bring their own manifests.
- Script bodies never need to be stored in Postgres.

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

Two viable approaches; whichever is chosen, **the database is authoritative**:

- **In-process scheduler** (e.g. `robfig/cron` in the supervisor). Simpler, one
  source of truth by construction.
- **Generated systemd timers.** More "native", but units must be treated as
  regenerable derived state — full regenerate-and-reload on change, never hand-edited.

Decision deferred to Phase 5. Quadlet is the right tool for the *platform's own*
services either way ([docs](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html));
note it requires cgroup v2.

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

Two credential types, resolved by one middleware into a common `principal`:

| Caller | Credential | Storage |
|---|---|---|
| Machines / CLI | Opaque token, prefixed (`sra_live_<random>`) | Only a hash in Postgres; plaintext shown once |
| Browsers (later) | Server-set session cookie after OIDC Authorization Code + PKCE | `HttpOnly`, `Secure`, `SameSite=Lax` |

**Never store JWTs in `localStorage`.** Any XSS becomes total account compromise and
they can't be revoked.

Scopes from day one, even before full RBAC: `read` / `run` / `admin`.

### 4.11 Web UI (deferred, Phase 7+)

Decision: a JavaScript SPA consuming the same API, **served from the same origin** as
the API (built with Vite, embedded into the Go binary with `//go:embed`).

The same-origin choice is not cosmetic. **`EventSource` cannot set custom headers** —
it issues GET only, with no way to attach `Authorization`
([whatwg/html#2177](https://github.com/whatwg/html/issues/2177)). A bearer-token SPA
on a different origin therefore cannot use the native SSE API for live logs; the
workarounds are tokens in query strings (logged, cached, in browser history), a
polyfill, or hand-rolling `fetch` + `ReadableStream` with a custom SSE parser.
Same-origin + cookie session means `new EventSource('/api/v1/runs/42/logs')` simply
works. External clients still use bearer tokens against the same endpoints.

Server-rendered HTML + htmx was considered and rejected: the form builder is a
genuine client-side application, and a single API with one client type is simpler
than two rendering strategies.

---

## 5. Data model sketch

Not final — refine during Phase 1–3. Included so the shape is visible.

```
principals    id, kind(user|token), name, token_hash, scopes[], created_at
repos         id, name, path, kind(local|external), remote_url
jobs          id, repo_id, manifest_path, name, runtime_id, enabled
runtimes      id, name, base_image, sys_packages, lang_manifest,
              image_digest, build_status, built_at
runs          id, job_id, principal_id, state, idempotency_key,
              commit_sha, image_digest, params_json,
              container_id, exit_code,
              queued_at, started_at, finished_at
run_logs      run_id, seq, stream(stdout|stderr), ts, text
schedules     id, job_id, cron_expr, timezone, next_due_at, enabled
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
| 8 | Shell out to git | Native tooling principle | Need fine-grained in-process control |
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

---

## 7. Deliberately deferred

Not forgotten — sequenced. See PLAN.md for when.

- Web UI / SPA
- Form builder
- OIDC / Authentik integration
- Full RBAC
- External git repo sync + webhooks
- Custom Containerfile upload
- Swagger UI
- Multi-node execution

---

## 8. Open questions

Unresolved. Resolve at the phase indicated.

| Question | Resolve at |
|---|---|
| In-process cron vs. generated systemd timers | Phase 5 |
| ~~Log retention: how long, and prune where (files vs Postgres)?~~ | **Resolved at task 2.2 — decision #18** |
| Image prune policy — time-based, count-based, or reference-based? | Phase 4 |
| Does PowerShell's AST parser give usable parameter introspection? Prototype needed | Phase 6 |
| Base image family — Alpine (small) vs Debian (compatible, esp. for PowerShell)? | Phase 4 |
