# Build Plan

**Companion document:** 
- ARCHITECTURE.md (the *what* and *why*; this file is the *when*)
- HISTORY.md (the *what* you did, *what* broke, *what* you
   were about to do next)

**Last updated:** 2026-08-06

---

## How to use this document

This project will be worked on in bursts with weeks of nothing in between. That is
fine, but it means the plan must survive being forgotten. Two rules:

1. **Update the "Current position" block below at the end of every session.** Even
   one line. This is the single most valuable thing in the file.
2. **Append to the session log at the bottom of HISTORY.md.** What you did, what
   broke, what you were about to do next. Future-you is a different person with no
   memory.

### Status markers

Update the marker on each task as it moves:

- `[ ]` not started
- `[~]` in progress
- `[x]` done
- `[!]` blocked or deferred — always add a note saying why

### Reading order after a long break

1. "Current position" block, below
2. The last 2–3 entries in HISTORY.md
3. The phase you're in
4. ARCHITECTURE.md section 6 (decision log) if a design choice feels arbitrary

---

## Current position

> **Update this block every session.**

- **Phase:** 9 — **complete, exit check passed live** (9.1–9.13 all landed
  and verified against a real Authentik instance: migration, sqlc,
  `internal/oidc`, login/callback handlers, bootstrap-admin, password
  surface removed, `/about` split, client/CLI/`cmd/seed` trimmed, web UI,
  tests, docs — plus one live-found bug fixed in the same session, see
  below). OIDC browser authentication replaces Phase 7's local password
  login outright (decision #29 superseded by #34) - no group claim read,
  roles stay local.
- **Next action:** no phase is currently open. Phase 8's own carried-over
  item is still outstanding - promote the exit check's curl-based
  permission assertions (viewer 403s, operator's read/write split) into
  real `internal/api/*_test.go` cases. Otherwise, pick from ARCHITECTURE.md
  §7's deferred list (per-schedule service tokens, a web setup wizard for
  first-run OIDC config, external git repo sync + webhooks, or 7.8's own
  deferred follow-ups).
- **Live-found and fixed this session:** sign-out only cleared this app's
  own session cookie, leaving Authentik's SSO session alive - the next
  "sign in" click silently re-authenticated with no prompt. Fixed via
  RP-Initiated Logout (migration `00014_session_id_token.sql` stores the
  ID token at login purely to hand back as `id_token_hint` at logout). See
  Phase 9's "Exit check" note above and HISTORY.md for the full story,
  including two Authentik-specific configuration gotchas hit along the way.
- **Off-plan work since Phase 8:** the web SPA (`web/src/`) was migrated from
  raw unstyled HTML to Mantine v9 (`@mantine/core`/`hooks`/`notifications`/
  `form`) - an `AppShell` sidebar layout, a shared `statusColor` Badge helper,
  a one-time-secret reveal `Modal` (token/password creation), and every
  page's tables/forms converted to Mantine components. Not a numbered PLAN.md
  phase - purely a visual pass, no API/behavior changes initially.
  Browser-verified with a headless Chromium in both light and dark color
  schemes. Two follow-ups landed in the same off-plan thread: (1) "New X"
  create forms and Settings' password form, which read as loose inputs
  flush against the sidebar, wrapped in a bordered `Paper` card; (2) a new
  `/` Dashboard page (Runs moved to `/runs`) showing a KPI row (currently
  queued; succeeded/failed/cancelled/lost since a 24h window) plus "last
  login" - this one **did** touch the API: migration `00010` adds
  `principals.last_login_at` (set by `LoginHandler` only, never on token
  auth), and a new `GET /api/v1/runs/stats` endpoint (`RunStatsHandler`,
  `runs:read`-gated) backs the KPI row. Both got Go client parity
  (`internal/client`) alongside the API per the project's established
  API→client→CLI→web sequencing, though no CLI subcommand was added for
  either (web-only ask). See HISTORY.md for the full rationale, especially
  the last-login "previous value, not now" sequencing trick.
  (3) A "System status" tile row on the same Dashboard (database/Podman/
  supervisor up-or-down), plus a new Configuration page for editing
  `DATABASE_URL`/`PODMAN_SOCKET` at runtime, plus a Settings restructure
  into sub-tabs (Account/Users/Tokens/Configuration under one `/settings/*`
  URL tree, `/users`/`/tokens` now redirect there). New pieces: a
  `supervisor_heartbeat` singleton table (migration `00011`) the supervisor
  upserts every 5s and the API reads for staleness (>15s stale = "down"),
  backing `GET /api/v1/system/status` (`SystemStatusHandler`,
  `RequireAuth`-only, no specific permission); a new `internal/appconfig`
  package loading a dedicated `config.env`-style file (default
  `$HOME/.config/descendence/config.env`, override via
  `DESCENDENCE_CONFIG_FILE`) that both `cmd/api` and `cmd/supervisor` consult
  for `DATABASE_URL`/`PODMAN_SOCKET` - an actual environment variable of the
  same name still always wins, and neither process hot-reloads the file, so
  a Configuration-page save only takes effect on the next restart of both;
  `GET`/`PUT /api/v1/config` (new `config:read`/`config:write` permissions,
  migration `00012`, admin-role-only) backing the new page, with the stored
  password masked on read and preserved (not clobbered with the literal
  placeholder) if a client resubmits the mask unchanged. See HISTORY.md for
  why `DATABASE_URL` couldn't just move into a `settings` Postgres table
  (the bootstrap problem) and the reasoning behind the 5s/15s heartbeat
  cadence.
- **Blocked on:** nothing.
- **Notes carried from 7.6/7.7 - resolved this phase:** the dev Postgres
  instance's `kind='user'` principal `webui-716` (owns runs 245/246,
  `runs.principal_id` is `ON DELETE RESTRICT`) was flagged as needing "a real
  RBAC/user-management pass" to be handled properly rather than left as a
  permanent oddity. That pass has now landed: `webui-716` was backfilled to
  the `operator` role by migration `00009_rbac.sql` and, like every
  principal, is now revocable via `DELETE /api/v1/users/{id}` (soft-revoke)
  whenever its access should actually end - no longer a one-off DB
  situation, an ordinary case the new tooling handles. Separately, the
  `ui-test-runtime` runtime row (created and pruned during 7.7's live
  verification) still exists with `imagePruned: true` - by design (runtimes
  rows survive a prune the same way `runs.logs_pruned_at` does, decision in
  ARCHITECTURE.md §5), not leftover mess.
- **Notes carried from Phase 8:** every principal minted during this
  session's live verification (8.1-8.11, names prefixed `rbac-verify-`,
  `cli-test-`, `webui-verify-`, `exit-check-8-`) was deleted afterward - none
  of them ever triggered a run, so none hit the `webui-716` situation above.
- **Notes carried from 7.8:** the `library` repo (id 16) now permanently
  carries an `exit-check-greet` job from this session's live verification,
  the same way earlier sessions left `greet-params-smoketest` and similar -
  harmless, real params/form: exercised end to end, left in a valid
  (non-erroring) state. The two principals minted for this session's exit
  check (`exit-check-7-8`, `exit-check-web-716`) were deleted afterward, since
  neither owned any run history the way `webui-716` does.
- **Phase 6 summary** (complete, 6.1–6.7, exit check passed): Jobs take typed, validated parameters end to end. The manifest's
  `params:` block (name/type/required/default/secret) is real
  (`internal/manifest`, `internal/manifest/params.go`); submitted values are
  resolved against it server-side (`manifest.ResolveParams`, called from
  `createJobRun`) and stored on the run in contract order (`runs.params_json`
  is a JSON *array* of `{name, value}`, not an object — Bash's positional-arg
  shim needs an order guarantee a JSON object's keys don't give). A job with
  params and no explicit `command:` routes its argv through a per-language
  shim (`internal/manifest/shims`: Bash/Python/PowerShell) that re-invokes
  the script with a native calling convention. `type: mount` params (Podman
  secrets, ARCHITECTURE.md §4.6) get their own storage
  (`runs.secret_params_json`) split out at resolution time, never assembled
  into `params_json` at all — the supervisor creates one Podman secret per
  mount param before container create and removes it alongside the container
  on every exit path (`cmd/supervisor/execute.go`). 6.7's PowerShell AST
  introspection prototype confirmed the technique works (decision #28) but
  stayed a spike — no code from it merged, nothing wired into jobsync or
  manifest parsing; revisit it when Phase 7's form builder wants a
  best-effort "suggest the fields" affordance.
- **Notes:** invariants live in CLAUDE.md's "Invariants worth not breaking" —
  that's the one place to check before touching jobs, git, or run execution.
  (The one fact from Phase 3 that isn't an invariant and has no other home:
  a supervisor process still executes runs strictly one at a time, which task
  1.15's HISTORY.md entry already flags as the most likely thing to bite once
  concurrency or scheduling arrives — Phase 5 hit exactly this: a schedule's
  `overlap_policy` of `queue` and `concurrent` are behaviorally identical
  today, see decision #27.) From Phase 4: this environment's podman
  container network blackholes IPv6 rather than rejecting it, so any
  .NET-based tool (PSResourceGet included) hangs until its IPv6 attempt
  times out before falling back to IPv4 - `DOTNET_SYSTEM_NET_DISABLEIPV6=1`
  is now baked into every PowerShell runtime's Containerfile for exactly this
  reason (`internal/runtimebuild/render.go`). From Phase 5: a systemd user
  service's `PATH` does not include `~/.local/bin` even though an
  interactive shell's does — `DESCENDENCE_CLI_PATH` exists specifically so a
  generated schedule unit's `ExecStart` can name the CLI's absolute path
  instead of relying on `PATH` resolution, found the hard way when the first
  live-fired schedule failed with "Unable to locate executable 'descendence'".
  From Phase 6: a first attempt at `runs.secret_params_json` (task 6.6) tried
  to keep it out of every `RETURNING`/`SELECT` list as the safety mechanism
  against an API response leaking a secret — this backfired immediately:
  sqlc stops reusing a single `store.Run` type the moment different queries
  against the same table select different column sets, and generates a
  distinct `*Row` struct per query instead, breaking every call site that
  assumed `store.Run` was universal. The actual safety boundary was always
  the Go response structs (`runResponse` has no field for the secret column,
  so `encoding/json` cannot serialise what nothing ever assigned to it) —
  not which SQL columns a query happens to select. Fixed by selecting it
  everywhere, like `params_json`, and let the type system do the enforcing.
  Also: Podman's `POST /libpod/secrets/create` returns `200`, not `201`, on
  this podman version — found by an unexplained "unexpected status 200 OK"
  the first time a mount-type param actually ran. From Phase 7: the same
  sqlc row-type lesson recurred immediately in a new place — see task 7.3's
  writeup above for the cookie-session variant (a join query, not a select
  list this time). Also: browsers do accept a `Secure`-flagged cookie over
  plain `http://localhost` (both Chrome and curl in this environment) —
  `localhost` is treated as a potentially trustworthy origin, so dev-mode
  testing over HTTP did not require relaxing the cookie flags.

---

## Overall shape

Eight phases. Phases 0–4 are the real project; 5–7 are extensions. Each phase ends
with something that *works* — never "half a layer everywhere".

| Phase | Goal | Rough size |
|---|---|---|
| 0 | Foundations — tools installed, skeleton repo, Postgres running | 1–2 sessions |
| 1 | **Vertical slice**: CLI → API → Postgres → supervisor → container → exit code | 4–8 sessions |
| 2 | Log capture and streaming | 3–5 sessions |
| 3 | Jobs + git library | 4–6 sessions |
| 4 | Runtimes (image building) | 3–5 sessions |
| 5 | Scheduling | 2–4 sessions |
| 6 | Parameters | 3–5 sessions |
| 7 | Web UI | large, open-ended |

A "session" is a few hours of free time. These are guesses; do not treat slippage as
failure.

**The most important phase is 1.** It is tempting to rush past because "run a
container and record the exit code" sounds trivial. It is not — container lifecycle,
cancellation and crash recovery are what make this thing trustworthy, and they are far
easier to get right before five features sit on top of them.

---

## Phase 0 — Foundations

**Goal:** every tool installed and verified, an empty-but-running skeleton.
**Done when:** `go run ./cmd/api` serves a health endpoint, and `DBeaver` connects.

- [x] **0.1** Install Go (latest stable). Verify: `go version`.
- [x] **0.2** Install Podman. Enable the rootless socket:
      `systemctl --user enable --now podman.socket`.
      Verify: `curl --unix-socket $XDG_RUNTIME_DIR/podman/podman.sock http://d/v5.0.0/libpod/info`
      returns JSON. *If this fails, stop and fix it — everything depends on it.*
- [x] **0.3** Enable lingering so the user service survives logout:
      `loginctl enable-linger $USER`.
- [x] **0.4** Run Postgres in a container via a Quadlet unit (`.container` file in
      `~/.config/containers/systemd/`). This is both the database and your first
      hands-on Quadlet exercise. Verify: connect with `DBeaver`.
- [x] **0.5** Create the git repo. Minimum layout:
      ```
      cmd/api/          cmd/supervisor/     cmd/cli/
      internal/store/   internal/podman/    internal/api/
      api/openapi.yaml  migrations/         README.md
      docs/ARCHITECTURE.md   docs/PLAN.md
      ```
- [x] **0.6** `go mod init`, then a `cmd/api` that serves `GET /healthz` → `200 OK`.
- [x] **0.7** Pick and set up a migration tool (`goose` or `golang-migrate`). Write
      migration 001 creating a single throwaway table; confirm up and down both work.

> **Beginner note.** Migrations are versioned SQL files that build the schema step by
> step. You want them from the very first table — retrofitting is miserable, and
> "how do I recreate my database" should always have the answer "run the migrations".

**Exit check:** you can destroy the Postgres container, recreate it, run migrations,
and be back where you started. If that isn't true, fix it now.

---

## Phase 1 — Vertical slice

**Goal:** one run, end to end, no git, no schedule, no forms.
**Done when:** `cli run --image alpine --cmd 'echo hello'` prints the exit code, and
the run appears in Postgres with correct timestamps and state.

### 1a. Data and API skeleton

- [x] **1.1** Migration: `principals` and `runs` tables. Use the sketch in
      ARCHITECTURE.md §5, trimmed to what Phase 1 needs.
- [x] **1.2** Set up `pgx` (Postgres driver) and `sqlc` (generates typed Go from your
      SQL). Write one query, generate, call it.
- [x] **1.3** Write `api/openapi.yaml` with exactly three operations:
      `POST /api/v1/runs`, `GET /api/v1/runs/{id}`, `GET /api/v1/runs`.
- [x] **1.4** Write routing and handlers directly in `internal/api`, no chi, no codegen, for
      learning value.
- [x] **1.5** Auth middleware: read `Authorization: Bearer`, hash it, look up the
      principal, reject with 401 if unknown. One bootstrap token, inserted by a
      migration or a `make seed` target.

> **Why auth now, when it's a single-user tool?** Because every handler needs a
> `principal_id` to stamp onto records. Adding the concept later means touching every
> handler and backfilling every table. It's ~30 lines today.

- [x] **1.6** Implement `POST /api/v1/runs`: validate, insert a row with state
      `queued`, return `202 Accepted` with the ID and a `Location` header.
- [x] **1.7** Implement `GET /api/v1/runs/{id}`.
- [x] **1.8** Honour `Idempotency-Key`: unique index on it; a repeat returns the
      original run rather than creating a second.

### 1b. Podman client

- [x] **1.9** `internal/podman`: an HTTP client over the Unix socket
      (`http.Client` with a custom `DialContext`). First call: `/libpod/info`.
- [x] **1.10** Implement create / start / wait / remove container.
      **Always label the container with `run_id`.**
- [x] **1.11** Build argv as a **`[]string`, never a shell string.** Write a test that
      passes `; rm -rf /` as an argument and asserts it is treated as literal text.

### 1c. Supervisor

- [x] **1.12** Claim loop: poll for `queued` runs using
      `SELECT ... FOR UPDATE SKIP LOCKED`, mark `running`, record `started_at`.
- [x] **1.13** Execute: create the container, start it, wait, record `exit_code`,
      set the terminal state, `finished_at`. Remove the container.
- [x] **1.14** Implement all six states: `queued`, `running`, `succeeded`, `failed`,
      `cancelled`, `lost`.
- [x] **1.15** **Reconciler.** On startup, list containers filtered by the `run_id`
      label and compare against runs in non-terminal states. Adopt what's still
      alive; mark the rest `lost`.
      Runs synchronously before the claim loop starts, so a long-running
      adopted run currently delays new queued runs from being claimed.
- [x] **1.16** Advisory lock so a second supervisor refuses to start (or waits).
- [x] **1.17** Timeouts: a per-run maximum duration, enforced with `context.Context`;
      on expiry, kill the container and record `failed` with a reason.

### 1d. CLI

- [x] **1.18** Hand write the API client from the same `openapi.yaml`.
- [x] **1.19** `cli run` — creates a run and polls until terminal, printing state.
- [x] **1.20** `cli runs list`, `cli runs get <id>`.
- [x] **1.21** Config: server URL and token from env vars or `~/.config/<name>/config`.

### 1e. Prove it

Do these deliberately — they are the point of the phase.

- [x] **1.22** Kill the supervisor mid-run, restart it. The reconciler behaves correctly.
- [x] **1.23** Kill the API mid-poll. Runs continue unaffected.
- [x] **1.24** Submit 20 runs at once. All complete, none run twice.
- [x] **1.25** No leaked containers: `podman ps -a` is clean after everything finishes.

**Exit check:** all of 1.22–1.25 pass. Do not move on until they do.
**→ Passed.** Phase 1e also surfaced three real defects; see the session
log. That is what the phase is for – all four checks passing untouched on
the first attempt would have meant they were too shallow.

---

## Phase 2 — Logs

**Goal:** see what a script printed, live and afterwards.
**Done when:** `cli run --follow` streams output as it happens, and closing/reopening
resumes without gaps.

- [x] **2.1** Attach to container output in the supervisor; write to a per-run file
      with sequence numbers.
- [x] **2.2** Record log metadata (seq, stream, timestamp) in Postgres; keep bodies
      in files. Decide and write down the retention policy (open question in
      ARCHITECTURE.md §8).
- [x] **2.3** Fan-out: one attach per run, N subscribers. Bounded channels; drop slow
      consumers rather than blocking the writer.
- [x] **2.4** `GET /api/v1/runs/{id}/logs` — historical, paginated.
- [x] **2.5** Same endpoint with `Accept: text/event-stream` → SSE. Emit `id:`,
      `event:`, `data:` with a blank line between messages; one `data:` line per
      output line. Call `Flush()` after every message.
- [x] **2.6** Honour `Last-Event-ID` on reconnect: replay from that sequence number.
- [x] **2.7** Exit the stream goroutine on `r.Context().Done()` — otherwise every
      closed client leaks a goroutine.
- [x] **2.8** `POST /api/v1/runs/{id}/cancel` — propagate cancellation, stop the
      container, record `cancelled`.
- [x] **2.9** CLI `--follow`.

> Get cancellation and context propagation right **here**. It gets much harder once
> scheduling and parameters are layered on.

**Exit check:** a 60-second script streams live; disconnect and reconnect mid-run and
no lines are lost or duplicated; cancel works within a second or two.
**→ Passed**, through the real CLI against the real stack:

- **Streams live.** A 60s script's lines arrived at the rate they were
  printed (7 → 10 lines over 3 seconds of a 1-line-per-second script).
- **Disconnect and reconnect.** Killing the follower mid-run and starting
  another gave a dense 1..60 with no duplicates. Resuming from an exact
  position split a 30-line run 17 + 13 across the seam — joined, still
  dense 1..30, still no duplicates. Killing and restarting the *API* twice
  underneath a live follower cost 40 of 40 lines nothing.
- **Cancel is prompt.** 1.02s, three times running, measured end to end
  from the CLI.

Phase 2 also cost two silent-data-loss defects in the capture that had
been there since 2.1 (decisions #20 and #21) — see HISTORY.md. Neither
produced an error anywhere; both were found only by printing 20000 lines
instead of three.

---

## Phase 3 — Jobs and git

**Goal:** named, reusable jobs backed by version-controlled scripts.
**Done when:** `descendence jobs run backup-db` works, and the run records the
commit SHA. (Built as `jobs`/`repos`, plural — matching the existing `runs`
command rather than this line's original `cli job`; see task 3.6.)

- [x] **3.1** Migration: `repos`, `jobs`. Add `job_id` and `commit_sha` to `runs`.
      `image_ref` is nullable with a CHECK of "image or runtime", so Phase 4
      can add runtimes without altering a NOT NULL column.
- [x] **3.2** Create and manage a bare git repo on disk by using `go-git` implementation of git.
- [x] **3.3** Define the sidecar manifest format (`<name>.job.yaml`) — start minimal:
      name, script path, runtime, description.
- [x] **3.4** Scan a repo, parse manifests, sync into the `jobs` table.
- [x] **3.5** `POST /api/v1/jobs/{id}/runs` — resolve the current commit SHA, copy the
      script into the container, record the SHA on the run.
- [x] **3.6** Job CRUD endpoints + `cli job list/get/run`.
- [x] **3.7** Upload a script through the API → commit to the repo with the calling
      principal as author.

**Exit check:** change a script, run it, and the new run's `commit_sha` differs from
the previous one. You can check out any past SHA and see exactly what ran.
**→ Passed**, through the real API and CLI against the real stack:

- Uploaded `hello.sh` + `hello.job.yaml`, synced, ran: output captured,
  `jobId` and `commitSha` recorded, `argv` was `["/run/job/hello.sh"]` with
  no interpreter named anywhere.
- Edited the script, ran again. The two runs carry **different**
  `commit_sha`, and `git show <sha>:scripts/hello.sh` at each one returns
  exactly the source that produced that run's output.
- Exit codes propagate: a script exiting 3 gives `descendence jobs run` an
  exit of 3; a run that failed because its script was missing exits 1.
- A disabled job is refused with 409; `podman ps -a` clean afterwards, no
  containers carrying a `run_id` label.

**One addition beyond the listed tasks:** a lazy image pull. There was no
image endpoint at all, so the first run of a job on a fresh machine died with
an opaque "no such image" from container create. Now create is attempted,
and on that specific error the image is pulled once and create retried. No
digest resolution - that stays Phase 4. It is on `longPollClient`, which is
the **fourth** instance of the blanket-timeout trap; HISTORY predicted it
would show up in "whatever long-lived endpoint comes next", and it did.

---

## Phase 4 — Runtimes

**Goal:** scripts run in an environment with their dependencies.
**Done when:** a Python job using `requests`, and a PowerShell job using a PSGallery
module, both run successfully.

- [x] **4.1** Migration: `runtimes`. Add `runtime_id` and `image_digest` to `runs`.
      The table and both columns existed as skeletons since migration 00001;
      `00004_runtimes_build.sql` fleshed it out with `lang`, `input_hash`,
      `build_error`, `image_pruned_at`, and the claim/prune indexes.
- [x] **4.2** Choose base images. **Decision #25: Debian**, all three
      languages — see ARCHITECTURE.md §6.
- [x] **4.3** Containerfile template + renderer (`internal/runtimebuild`).
- [x] **4.4** Build via the Podman API (`internal/podman.BuildImage`); tagged
      with a hash of the inputs (`runtimebuild.InputHash`/`ImageTag`) so
      identical definitions dedupe. Supervisor runs a second, parallel claim
      loop (`cmd/supervisor/build.go`) alongside the run claim loop.
- [x] **4.5** Builds are async: `POST /runtimes/{id}/build` returns 202;
      `GET /runtimes/{id}` doubles as the poll target (`buildStatus` lives
      directly on the row, no separate build resource). Creating a runtime
      queues its first build automatically; the endpoint is for rebuilds.
- [x] **4.6** Resolve tag → digest after build (`podman.InspectImage`); runs
      pin the **digest** at creation (`CreateJobRunHandler`), never
      re-resolved. Manifest's `runtime:` key implemented
      (`internal/manifest`), resolved to a runtime row by `jobsync`.
- [x] **4.7** Prune policy: **manual, API-triggered**
      (`POST /runtimes/prune`, ids or an age threshold), plus the same
      "unused" rule swept automatically alongside the log-retention sweep
      (`RUNTIME_IMAGE_RETENTION_DAYS`, default 30). Shared logic in
      `internal/runtimeprune` so the two paths can't disagree.
- [x] **4.8** CLI `runtime list/get/create/build/prune`
      (`cmd/cli/runtimes.go`, `internal/client/runtimes.go`).

**Exit check:** both target jobs run; rebuilding a runtime does not change what an
already-scheduled run executes.
**→ Passed.** A `py-requests` (Python 3.12, `requests==2.32.3`) and a
`ps-nameit` (PowerShell 7.4, PSGallery module `NameIT`) runtime were both
created, built and run through real jobs (`py-check`, `ps-check`) against
the live stack: `requests` imported and printed its version; `NameIT`
imported and reported its version. `py-requests` was then rebuilt with a
changed manifest, producing a genuinely different image digest
(`sha256:3814d52d…` → `sha256:63db6b57…`); the earlier run's stored
`imageDigest` was re-fetched via the API and was byte-for-byte unchanged,
confirming a run's pinned digest survives a later rebuild by construction
(nothing ever writes `runs.image_digest` after insert).

One real environment finding, not a code defect but worth keeping: this
sandbox's podman container network blackholes IPv6 rather than rejecting
it, so PSGallery's `Install-PSResource` (built on .NET's `HttpClient`)
hung for 100s+ per attempt until `DOTNET_SYSTEM_NET_DISABLEIPV6=1` was
added to the rendered Containerfile — after which the same install
completed in under a second. `curl`/Python's `urllib` were unaffected,
which is what made this look like a PSGallery problem at first rather than
a container-networking one.

---

## Phase 5 — Scheduling

**Goal:** jobs run unattended.
**Done when:** a job runs every 5 minutes for an hour without intervention, including
across a supervisor restart.

- [x] **5.1** Resolved: **generated systemd (user) timers**, owned by the
      supervisor (decision #27) — not an in-process cron loop.
- [x] **5.2** Migration `00005_schedules.sql`: `catch_up_policy`,
      `overlap_policy`, `updated_at` on `schedules`; `schedule_id` on `runs`;
      drops the `next_due_at` skeleton column (nothing stores it under this
      design).
- [x] **5.3** Reinterpreted per 5.1: `internal/scheduling` (cron_expr →
      OnCalendar= translation + unit rendering), `internal/systemdunit`
      (`systemctl --user` wrapper), `cmd/supervisor/schedule.go` (the
      schedule-sync loop). No advisory-lock-guarded scheduler loop was
      needed — systemd itself does the firing, independent of the
      supervisor's liveness.
- [x] **5.4** `catch_up_policy` maps onto the generated timer's
      `Persistent=`. Verified live: stopping a timer, letting ~4 one-minute
      windows pass, then re-enabling it produced **exactly one** catch-up
      run, not one per missed window.
- [x] **5.5** `timezone` maps onto the timer's `TimeZone=` directive
      (separate from `OnCalendar=`); `robfig/cron/v3` computes an
      informational, display-only `nextDueAt` in the same zone.
- [x] **5.6** `overlap_policy` (`skip`/`queue`/`concurrent`) enforced in the
      trigger endpoint via `GetLatestRunForSchedule`. `queue` and
      `concurrent` are behaviorally identical today — the supervisor still
      executes runs strictly one at a time.
- [x] **5.7** Schedule CRUD (`POST`/`GET /api/v1/jobs/{id}/schedules`,
      `GET`/`PATCH`/`DELETE /api/v1/schedules/{id}`) as a plain Postgres
      write, plus `POST /api/v1/schedules/{id}/trigger` (the first endpoint
      in this codebase to enforce a scope) and `descendence schedule
      list/get/create/update/delete/trigger`.

**Exit check:** restart the supervisor mid-schedule; no duplicate and no missed runs.
**→ Passed**, through the real stack with a real systemd user timer firing
every minute: killed the supervisor mid-window, confirmed the timer still
fired and queued a run with the supervisor entirely down, restarted the
supervisor, and it claimed and executed that run with nothing duplicated or
lost. Also proved live: the overlap `skip` policy actually skipping a
second concurrent fire, `queue` allowing one, a scopeless token correctly
getting `403` on trigger, and `systemctl --user` cleanly removing a
schedule's unit files (and stopping its timer) on delete.

---

## Phase 6 — Parameters

**Goal:** jobs take input.
**Done when:** `cli job run greet --param name=World` works across Python, Bash and
PowerShell.

- [x] **6.1** Extend the manifest with the parameter contract (name, type, required,
      default). Also projects the contract onto a new `jobs.params_json`
      column (decision #23's pattern) so `GET /jobs/{id}` can expose it.
- [x] **6.2** Validate submitted params against the contract server-side; reject
      clearly on mismatch. `POST /jobs/{id}/runs` takes an optional
      `{params: {name: value}}` body; CLI gets `-param name=value`
      (repeatable).
- [x] **6.3** Mount params as JSON at `/run/job/params.json`.
- [x] **6.4** Write shims: Bash, Python, PowerShell. Shim = wrapper argv
      (`[shim, script]`, chosen by script extension), not a sourced
      library — only enters argv when the job actually declares params.
      `runs.params_json` is an ordered array of `{name, value}`, not an
      object: Bash's positional-arg order needs a guarantee a JSON
      object's key order (or Go map iteration) doesn't give.
- [x] **6.5** Store params on the run record for reproducibility — **redact anything
      marked secret**. Response-time redaction (fixed `"***"` sentinel),
      looked up against the job's contract by `run.JobID`.
- [x] **6.6** Wire Podman secrets as a parameter type (`type=mount`).
      `manifest.ResolveParams` splits mount-type values into
      `runs.secret_params_json` at resolution time — never assembled into
      `params_json` to begin with, so there's nothing for an API response
      to leak regardless of which SQL columns a query selects (the actual
      safety boundary is that `runResponse` has no field for it).
      Supervisor creates one Podman secret per mount param before
      container create, mounts it under `/run/job/secrets/<name>`, removes
      it alongside the container on every exit path. Verified live against
      the real stack (see HISTORY.md).
- [x] **6.7** *Optional:* prototype PowerShell AST introspection (open question in
      ARCHITECTURE.md §8, resolved as decision #28). `ParseFile` cleanly
      extracts a `param()` block's names/types/`ValidateSet`; two real
      gotchas found live (`Mandatory`'s value must be evaluated from its
      expression AST, not detected by presence; `DefaultValue` is raw
      source text, not a value — a non-literal default has nothing this
      platform can safely produce without executing script code). Spike
      only, per its own scope — no code merged, nothing wired into
      jobsync or manifest parsing; the finding lives in ARCHITECTURE.md.

**Exit check:** a parameter value containing `"; rm -rf /; #` is passed through
literally and harmlessly. **→ Passed**, verified live — see HISTORY.md.

---

## Phase 7 — Web UI

Deliberately vague; scoped properly on arrival per ARCHITECTURE.md §4.11. First pass
covers 7.1–7.5 (a read-only vertical slice); 7.6–7.8 are a later session.

- [x] **7.1** Vite + React + TypeScript project under `web/`. Scaffolded with
      `create-vite`; `web/embed.go` (package `webdist`) is a small Go file living
      inside the JS project root purely to hold the `//go:embed dist` directive -
      embed requires the pattern's directory to exist at compile time, so
      `web/dist/index.html` is a checked-in placeholder (`web/.gitignore` excludes
      the rest of `dist/`) that a real `npm run build` overwrites locally.
- [x] **7.2** TS types generated from `openapi.yaml` via `openapi-typescript`
      (`npm run gen:api` → `web/src/api/schema.ts`, regenerated the way `sqlc
      generate` regenerates Go). Request *logic* stays hand-written
      (`web/src/api/client.ts`'s `request()`, mirroring `internal/client`'s
      `do()`/`send()`/`requestOptions` shape) rather than fully codegen'd -
      splits PLAN's literal "generate the client" from decision #15's
      hand-written ethos instead of picking one over the other.
- [x] **7.3** Session cookie auth. `principals.kind='user'` now carries a bcrypt
      `password_hash` (migration `00008_web_auth.sql`), superseding migration
      00001's original comment that those rows were OIDC placeholders - OIDC
      stays deferred (§7), but "a login form against a local account" (this
      task's own note) arrived first. New `sessions` table, hash-only storage
      like `token_hash`. `RequireAuth` (`internal/api/auth.go`) now resolves
      *either* a Bearer token or a `descendence_session` cookie into the same
      `store.Principal`, so no existing handler changed. `POST
      /api/v1/auth/login` / `.../logout` added to `openapi.yaml`. `cmd/seed
      -kind user` mints the first local account, printing a password once like
      the existing token bootstrap.
- [x] **7.4** `web/dist` embedded into `cmd/api` (`web` package `webdist`),
      mounted as a catch-all at `/` behind every `/api/v1/*`, `/healthz` and the
      exact-match `GET /{$}` route - Go 1.22's mux always prefers the most
      specific pattern, so `/` still answers JSON server info for machine
      clients and only the SPA's own routes (`/login`, `/runs/42`, ...) hit the
      catch-all. `spaHandler` falls back to `index.html` for any path with no
      matching static file, so a browser refresh on a client-side route doesn't
      404.
- [x] **7.5** Read-only views: run list (cursor-paginated, `RunList`), run
      detail, live logs via the browser's native `EventSource` against `GET
      /api/v1/runs/{id}/logs` - confirmed live, verifying ARCHITECTURE.md
      §4.11's central claim end to end (see exit check below).
- [x] **7.6** Trigger runs. New `web/src/api/jobs.ts` (list/get/`createJobRun`,
      mirroring `internal/client/jobs.go`), a `JobList` page and a `JobDetail`
      page that renders a form from the job's `params` contract (`JobParam`:
      one input per param, checkbox for `bool`, `password` input for
      `secret`/`mount` types) and POSTs `/api/v1/jobs/{id}/runs` on submit -
      an omitted field lets the server apply its own default, same as
      `--param name=value` on the CLI. A disabled or deleted job's Run button
      is disabled client-side (the server's 409 is still the real guard). On
      success the new run's id navigates straight to `/runs/{id}`, reusing
      7.5's live-log view to watch it. A small top nav (`web/src/Layout.tsx`:
      Runs | Jobs | Sign out) now wraps every authenticated route.
- [x] **7.7** Job and runtime management. Job: an Enable/Disable button on
      both `JobList` (per-row) and `JobDetail`, calling the existing
      `PATCH /api/v1/jobs/{id}` (`enabled` is still the only field this
      endpoint can touch - decision #23). Runtime: new `web/src/api/runtimes.ts`
      (mirroring `internal/client/runtimes.go`), a `RuntimeList` page (table
      plus a "new runtime" form posting `RuntimeCreate`) and a `RuntimeDetail`
      page showing full build state with a Rebuild button
      (`POST /api/v1/runtimes/{id}/build`) that polls `GET
      /api/v1/runtimes/{id}` every 2s while `buildStatus` is non-terminal -
      there's no SSE equivalent for builds the way there is for run logs, so
      this is plain polling, not `EventSource`. `web/src/api/client.ts`'s
      `request()` was fixed alongside this: it assumed every 2xx response
      decodes as JSON, which broke on `buildRuntime`'s empty-body 202 - now
      reads the body as text first and only parses non-empty ones.
- [x] **7.8** Form builder. Shipped as YAML editing with a rendered preview,
      per this task's own note - drag-and-drop stays deferred. Two things had
      to exist first: `internal/manifest`'s `form:` key, decoded as a raw
      `yaml.Node` purely to be rejected since Phase 3, now real layout
      metadata (`FormSection`/`FormField`: title/help/ordered fields with
      optional label/help overrides) over the existing `params:` contract -
      presentational only, never a second source of what a param is; and a
      read path for a manifest's current content (`GET
      /api/v1/repos/{id}/files/{path...}`), since `createRepoFile` was
      write-only and editing an existing job needs its YAML first. Web UI:
      `web/src/pages/ManifestEditor.tsx` (`/jobs/new`, `/jobs/:id/edit`) is a
      YAML `<textarea>` (plain, no CodeMirror/Monaco - this codebase's
      minimal-dependency footprint outweighs syntax highlighting here) with a
      live preview pane, re-parsed every keystroke via a new `js-yaml`
      dependency (the one new runtime dependency this task needed - hand-writing
      a YAML parser is out of scope even for decision #15's ethos). The preview
      renders through `ParamField` (`web/src/paramField.tsx`), extracted from
      `JobDetail`'s inline per-type rendering so the preview and the real
      trigger form can never drift apart. Client-side parsing
      (`web/src/manifestPreview.ts`) is deliberately lenient - unlike the
      server's `Parse`/`validate`, an unresolvable reference is skipped, not
      an error, since this is a live view of a document expected to be
      mid-edit; the server (via `createRepoFile`'s auto-sync) stays sole
      authority over what actually commits. Create mode assumes the single
      seeded local repo rather than adding repo-picker UI nothing at homelab
      scale needs. This closes Phase 7.

**7.1–7.5 exit check**: verified against the real stack (Postgres, Podman, a live
supervisor). `cmd/seed -kind user` minted a local account; logging in via `curl`
set an `HttpOnly`/`Secure`/`SameSite=Lax` cookie; the cookie alone authenticated
`GET /api/v1/whoami`, `GET /api/v1/runs` and a `text/event-stream` request against
an existing run's logs (all four SSE `log` events plus the terminal `state` event
arrived correctly). Logging out cleared the session and the same cookie then 401'd.
Verified in both modes: `npm run dev` against a real `cmd/api` through the Vite
proxy, and the fully embedded production path (`npm run build` → rebuild `cmd/api`
→ no Vite process running at all) - root (`/`) still returns JSON, `/login` and
`/runs/42` both correctly fall back to `index.html`, and hashed JS/CSS assets serve
with correct content types. The existing CLI's bearer-token flow (`descendence runs
list`) was confirmed unaffected by the new cookie path.

One real bug caught in this verification, not just a plan: `GetPrincipalBySessionTokenHash`'s
join query gives sqlc a distinct row type (`GetPrincipalBySessionTokenHashRow`),
not `store.Principal` - exactly the Phase 6 `secret_params_json` lesson (same
*columns*, different Go *type*) recurring in a new place. Storing that row
directly into the request context made `principalFromContext`'s type assertion
fail silently, turning every cookie-authenticated request into a 500 ("no
principal in request context") instead of succeeding or 401ing - caught only by
actually calling `/api/v1/whoami` with a real cookie, not by `go build`/`go vet`.
Fixed by converting the row to `store.Principal` explicitly in `auth.go`.

**7.6 exit check**: verified against the real stack by issuing the exact requests
the new UI code makes. `POST /api/v1/jobs/69/runs` (a job with a required
`string` param and a required `mount`/secret param) with
`{"params":{"name":"UI Test","token":"sk-uitest"}}` - the same shape
`JobDetail`'s form submits - returned `202` with `Location: /api/v1/runs/245`,
and the run completed `succeeded` with `params: [{"name":"name","value":"UI
Test"}]` (the mount param correctly absent from `params_json`, per task 6.6).
`POST /api/v1/jobs/20/runs` with no body (a job with no params) also queued
correctly. The embedded production build was rebuilt afterward and serves
`/jobs` and `/jobs/69` with `200` and the SPA shell, alongside the existing
`/`, `/login`, `/runs/42` routes.

**7.7 exit check**: verified against the real stack by issuing the exact requests
the new UI code makes. `PATCH /api/v1/jobs/29` toggled `enabled` true then back
to false, matching `JobList`/`JobDetail`'s toggle handler exactly.
`POST /api/v1/runtimes` with `{"name":"ui-test-runtime","lang":"python",
"langManifest":"requests==2.32.3"}` (the shape `RuntimeList`'s create form
submits) returned `202` with the new runtime `pending`; the supervisor's real
build claim loop picked it up and it reached `ready` with a real image digest.
`POST /api/v1/runtimes/5/build` (the Rebuild button's call) queued a genuine
rebuild that also reached `ready` - confirming `buildRuntime`'s empty-body 202
response is handled correctly by the fixed `request()`. `POST
/api/v1/runtimes/prune` with `{"ids":[5]}` was rejected while a build was still
in flight (a real 200 with the runtime reported `"skipped"`, not an error) and
succeeded once the build was terminal, matching the one-build-slot-per-runtime
rule (task 4.5) and confirming the row survives a prune with `imagePruned`
alone flipping. The embedded production build serves `/runtimes` and
`/runtimes/5` with `200`.

**7.8 exit check**: verified against the real stack by issuing the exact
requests the new UI code makes, against the repository's one real local
repo (`library`, id 16). `POST /api/v1/repos/16/files` with a manifest
declaring `params:` (`who`/`shout`) and a matching `form:` block (the shape
`ManifestEditor` submits) returned `201` with `sync.added` naming the new
job; `GET /api/v1/repos/16/files/{path}` (URL-encoded, matching
`getRepoFile`) read the same content back at the new commit. Editing it -
changing `who`'s default and resubmitting to the same path - came back in
`sync.updated`, not `added`. A manifest with `form:` referencing a
nonexistent param was rejected in `sync.errors` with a message naming the
bad reference, proving the server stays authoritative over what actually
commits even though the client-side preview parser is lenient by design.
Repeated over both bearer-token and session-cookie auth (a fresh `cmd/seed
-kind user` account, logged in via `curl`), since the SPA only ever uses the
cookie path. The embedded production build (rebuilt after `npm run build`)
serves `/jobs/new` and `/jobs/:id/edit` as SPA routes. No browser was
available in this session to drive the React UI interactively - verification
covers the exact HTTP calls the new pages make and confirms the built
JS/CSS bundle serves correctly, not a rendered/clicked-through UI.

One real bug caught in this verification, not just a plan: `GetPrincipalByTokenHash`
had the same distinct-row-type problem `GetPrincipalBySessionTokenHash` hit
during 7.6 (same lesson, same fix, different query) - sqlc's regen during
7.1-7.5 gave it its own row type instead of `store.Principal`, so every
bearer-token request (the CLI included) had been 500ing with "no principal in
request context" instead of succeeding or 401ing, since `RequireAuth`'s
bearer-token branch stored that row directly into the context.
`principalFromContext`'s type assertion failed silently. Caught immediately by
calling `/api/v1/whoami` with a freshly minted token. Fixed the same way as
7.6's cookie-path bug: convert the row to `store.Principal` explicitly.

---

## Phase 8 — RBAC and user/token management

Scoped on arrival per ARCHITECTURE.md §6 decision #30: real `roles`/`permissions`
tables (not just enforcing the existing `scopes` array), fixed built-in roles
(`admin`/`operator`/`viewer`, not an admin-editable builder), exactly one role per
principal, global (not per-resource-instance) permissions. User and token
management span the API, the Go client, the CLI (flags + TUI) and the web UI.

- [x] **8.1** Schema + permission plumbing. Migration `00009_rbac.sql`:
      `roles`/`permissions`/`role_permissions`/`principal_roles`, fourteen
      seeded `resource:verb` permission keys, three seeded roles, and a
      backfill of every existing principal's `scopes` onto the nearest
      equivalent role before dropping the column (`admin` in scopes →
      `admin`; `run` → `operator`; else → `viewer` - a clean cutover, not a
      staged migration, since this is a single-operator deployment).
      `GetPrincipalPermissions` (one indexed join) resolved once per request
      by `RequireAuth` alongside the principal; new `RequirePermission(key,
      handler)` middleware composes after it in the route table.
      `TriggerScheduleHandler`'s inline `principalHasScope` check - the only
      authorization check anywhere in the codebase before this phase - was
      replaced by the same middleware. `cmd/seed` gained `-role` (replacing
      `-scopes`), remaining the bootstrap escape hatch: the very first admin
      can't be created through an API that requires `users:write` to create
      anyone. Also fixed the duplicated sqlc-row-to-`store.Principal`
      conversion in `auth.go` (the bug 7.1-7.5 and 7.6 each hit once,
      independently) into one shared `assemblePrincipal` helper.
- [x] **8.2** Users API. `GET/POST /api/v1/users`, `GET/PATCH/DELETE
      /api/v1/users/{id}`, `PATCH /api/v1/users/me/password`. Admin-only
      (`users:read`/`users:write`) except self password-change, gated by
      "acting on self" inline rather than a permission key. `DELETE` is a
      soft-revoke (`revoked_at`), never a hard delete - `runs.principal_id`
      is `ON DELETE RESTRICT`. Password generated server-side and returned
      once when omitted on create.
- [x] **8.3** Tokens API. `GET/POST /api/v1/tokens`, `GET/DELETE
      /api/v1/tokens/{id}` - the API-driven equivalent of `cmd/seed -kind
      token`, closing the gap that tool minting was the only way to create
      one. Same admin-only/soft-revoke shape as users.
- [x] **8.4** Roles read API. `GET /api/v1/roles`, `GET /api/v1/roles/{name}`
      - list-only, no create/edit/delete (decision #30). `whoami`'s response
      extended with the resolved role name and permission list.
- [x] **8.5** OpenAPI spec + client codegen. New paths/schemas for
      users/tokens/roles; `Principal`'s flat `scopes` enum replaced by
      `role`/`permissions`. Opportunistically documented `sessionAuth` as an
      OpenAPI security scheme - `RequireAuth` has always accepted a session
      cookie alongside a bearer token, but the spec only ever declared
      `bearerAuth`. Regenerated `web/src/api/schema.ts`.
- [x] **8.6** Retrofit every existing jobs/runs/schedules/repos/runtimes
      route with the matching `RequirePermission(...)` call - previously any
      authenticated principal had full access to all of them. Route-table-only
      change (`cmd/api/main.go`); no handler body touched.
- [x] **8.7** Go API client. `internal/client/{users,tokens,roles}.go`,
      hand-written to mirror the new OpenAPI schemas (decision #15).
- [x] **8.8** CLI flag commands. `descendence user
      {list,get,create,set-role,revoke,passwd}`, `descendence token
      {list,get,create,revoke}`, `descendence role {list,get}`. `user passwd`
      prompts interactively via `charmbracelet/x/term`'s `ReadPassword` (no
      `-current`/`-new` flags, so a password never lands in shell history)
      and refuses to run off a non-TTY.
- [x] **8.9** CLI TUI. A "Users" menu entry, gated on the caller's resolved
      role from a synchronous `WhoAmI` call in `runUI` before the model is
      built - hidden rather than shown-and-403'd on selection, since the TUI
      is navigable and a dead-end action reads worse than an absent one. The
      screen itself is a read-only browse table; create/set-role/revoke stay
      flag-command-only.
- [x] **8.10** Web UI. `UserList`/`UserDetail` (admin-only create form and
      role reassignment, generated password shown once), `TokenList`
      (admin-only create form, plaintext token shown once, per-row revoke),
      `Settings` (self password-change - the one page every authenticated
      user can reach, not gated on `users:write`). `Layout.tsx`'s
      Users/Tokens nav links gated on `principal.permissions.includes
      ('users:read')`, matching the TUI's hide-don't-403 posture; pages
      further gate create/reassign/revoke controls on `users:write` since a
      viewer has `users:read` (can browse) but not `users:write`.
- [x] **8.11** Docs and exit check (this entry).

**Exit check**: verified against the real stack with fresh admin/operator/viewer
tokens. Viewer: `GET /jobs` 200, `POST /schedules/{id}/trigger` 403, `PATCH
/jobs/{id}` 403, `POST /runtimes` 403, `POST /repos` 403, `POST /users` 403.
Operator: `POST /schedules/{id}/trigger` 404 (permission granted, schedule
doesn't exist - not 403), `POST /users` 403, `POST /runtimes` 403. Admin: `GET
/jobs`/`/users`/`/tokens`/`/roles` all 200. Every layer was also exercised
individually during 8.1-8.10: migration backfill left no roleless principal;
create/list/patch-role/revoke round-tripped for both users and tokens through
the API, the CLI, and (via cookie-authenticated curl standing in for a browser,
since no browser is available in this environment) the exact calls the web
pages make; a revoked principal's next auth attempt 401s while its run history
stays queryable; self password-change worked end to end including a
subsequent login with the new password.

No new automated Go tests were added for the permission-denied paths (viewer
403s, operator's read/write split) - covered by the exit check's live
verification instead, matching how `TriggerScheduleHandler`'s original scope
check was verified in earlier phases. A follow-up worth doing before this
grows further: promote the exit-check's curl sequence into
`internal/api/*_test.go` cases so a regression doesn't require another manual
pass.

## Phase 9 — OIDC browser authentication

**Goal:** the browser logs in through any OIDC provider; local password auth is gone.
**Done when:** an IdP user logs in, gets a session, and an admin can assign them a
role — with no `password_hash` column left in the database.

Scope is OIDC only; per-schedule service tokens and a web setup wizard were split
out into later phases. The IdP authenticates, roles stay local in `principal_roles`
— no group claim is read, which is what keeps this provider-agnostic. Supersedes
decision #29.

- [x] **9.1** Audit first: how many `kind='user'` principals exist, and which are
      referenced by `runs.principal_id` (`ON DELETE RESTRICT`)? Decides 9.2's
      delete-vs-revoke split — see `webui-716` in Phase 7's notes.
- [x] **9.2** Migration `00013_oidc.sql`: drop `password_hash` and its CHECK; add
      `oidc_issuer`/`oidc_subject`, `UNIQUE` on the pair, plus a CHECK tying them
      to `kind='user'` the way `token_hash` is tied to `kind='token'`. Existing
      users: delete those with no runs, soft-revoke the rest. `Down` cannot
      restore hashes — say so in the file.
- [x] **9.3** sqlc: lookup by `(oidc_issuer, oidc_subject)`, JIT insert; delete the
      password-only queries.
- [x] **9.4** `internal/oidc`: discovery, `oauth2.Config`, ID token verifier, built
      once at `cmd/api` startup from `OIDC_ISSUER_URL`, `_CLIENT_ID`,
      `_CLIENT_SECRET`, `_REDIRECT_URL` (default
      `http://127.0.0.1:8080/api/v1/auth/callback`), `_SCOPES` (default
      `openid profile email`). Discovery failure is fatal, not degraded. New deps:
      `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`.
- [x] **9.5** `GET /api/v1/auth/login`: state, nonce and PKCE verifier into 5-minute
      `SameSite=Lax` cookies, then 302 to the authorization endpoint. Lax not
      Strict — the IdP returns via a top-level navigation. Replaces
      `POST /api/v1/auth/login`.
- [x] **9.6** `GET /api/v1/auth/callback`: verify state → exchange with PKCE →
      verify ID token → verify nonce → resolve `(iss, sub)` → mint the existing
      session cookie → 302 to `/`. A revoked subject is refused, never
      resurrected. An unknown subject is JIT-provisioned with **no role**; `name`
      comes from `preferred_username` (fallback `sub`; handle the UNIQUE collision)
      and is never refreshed on later logins.
- [x] **9.7** `OIDC_BOOTSTRAP_USERNAME`: a matching `preferred_username` gets `admin`
      in the same transaction as the JIT insert, at creation only.
- [x] **9.8** Remove the bcrypt login path, `ChangeOwnPasswordHandler`,
      `PATCH /api/v1/users/me/password`, and `POST /api/v1/users` — an
      admin-created user has no `oidc_subject` and could never log in. Role patch
      and revoke stay. Update `openapi.yaml` in the same change.
- [x] **9.9** `GET /` serves the SPA (drop the `GET /{$}` route); server info moves
      to `GET /about`, beside `/healthz`. Update `internal/client.Info()`,
      `openapi.yaml`, and regenerate `web/src/api/schema.ts`.
- [x] **9.10** `internal/client` and CLI: drop user-create and password-change.
      `cmd/seed` loses `-kind user`; its token path is now the sole recovery route
      with the IdP down.
- [x] **9.11** Web UI: the login page becomes a navigation to
      `/api/v1/auth/login` (not `fetch` — an XHR can't follow the redirect to the
      IdP); remove the password form and the Users create form; render one
      explanatory screen when `whoami` returns zero permissions instead of a wall
      of 403s.
- [x] **9.12** Tests: state mismatch, nonce mismatch, revoked subject refused, JIT
      creates a roleless principal, bootstrap match assigns `admin`. Phase 8's
      missing permission tests are already a flagged gap — don't widen it.
- [x] **9.13** Docs: ARCHITECTURE.md §4.10 (authentication half only), §6 new
      decisions with #29 marked superseded, §7 loses the OIDC item; `.env.sample`;
      HISTORY.md, including the authentik provider config that actually worked.

**Exit check:** fresh DB, `OIDC_BOOTSTRAP_USERNAME` set, real browser login → a
principal created with `admin`, landing on `/`. Log out, cookie cleared. A second
IdP user logs in → the roleless screen, not 403 toasts; admin assigns `viewer`;
viewer reads and cannot write. Revoke that user and log in again → refused, no new
principal, no session. Stop the IdP: `descendence runs list` and `cmd/seed` still
work. Wrong `OIDC_ISSUER_URL` → non-zero exit, not a half-started server.
`GET /about` returns JSON, `GET /` returns the SPA. Use `127.0.0.1` everywhere —
the session cookie is host-scoped, and mixing it with `localhost` breaks login
silently.

**→ Passed, live, against a real Authentik instance.** A real browser login
(headless Chromium, driven end to end since the operator's Authentik password
was shared for this purpose) with `preferred_username` matching
`OIDC_BOOTSTRAP_USERNAME` landed a principal with `admin` on `/`. A second
account JIT-provisioned roleless and hit the roleless screen, not 403s; an
admin (bearer-token, per decision #30's role-patch path) assigned it `viewer`;
the browser session then read `GET /api/v1/jobs` `200` and got `403` on `POST
/api/v1/repos`. Revoking it and re-attempting login returned `403` from the
callback with no session cookie set and no resurrected/duplicate principal row.
`descendence runs list` (bearer token) kept working through a real Authentik
outage (confirmed both by code inspection - zero references to `internal/oidc`
anywhere in `cmd/cli`/`cmd/seed`/`internal/client` - and live, after recovering
from an unrelated mishap, see below). A deliberately wrong `OIDC_ISSUER_URL`
made `cmd/api` exit non-zero via `log.Fatal`, never serving a request. `GET
/about` returns JSON, `GET /` returns the SPA.

One real bug found live, not just verified: sign-out only cleared this app's
own session cookie, leaving Authentik's SSO session alive, so the next "sign
in" click silently re-authenticated with no prompt at all. Fixed the same
session - see HISTORY.md and the "Fix logout" commit.

Two Authentik-specific gotchas, not code defects, worth keeping for the next
person setting this up:
1. Disabling every OAuth2 grant type except "password" on the provider (done
   mid-session while poking at something unrelated) makes even a *minimal,
   correctly-formed* `GET /authorize?response_type=code&...` fail with a
   generic `invalid_request: The request is otherwise malformed` - no
   redirect_uri/client_id-specific error, so it reads exactly like a
   misconfigured redirect URI. Recreating the provider from scratch (rather
   than continuing to debug the existing one) was faster than finding this.
2. RP-Initiated Logout's `post_logout_redirect_uri` is validated against the
   *same* Redirect URIs/Origins list as the OAuth2 flow's `redirect_uri` (at
   least on this Authentik version) - a second strict entry for the app root
   (`http://127.0.0.1:8080/`) was needed alongside the callback path. Newer
   Authentik versions may expose a dedicated logout-URI field instead; either
   works.

Also worth a note for future-me: `podman stop`-ing Authentik's containers
directly (rather than through its systemd Quadlet units,
`authentik-pod.service` et al.) tore the pod down instead of pausing it -
recovered cleanly via `systemctl --user start authentik-pod.service`, no data
lost, but a reminder that this instance is Quadlet-managed like the project's
own Postgres and should always be stopped/started that way.