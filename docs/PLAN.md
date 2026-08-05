# Build Plan

**Companion document:** 
- ARCHITECTURE.md (the *what* and *why*; this file is the *when*)
- HISTORY.md (the *what* you did, *what* broke, *what* you
   were about to do next)

**Last updated:** 2026-08-05

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

- **Phase:** 5 — **complete** (5.1–5.7, exit check passed). Next up is
  Phase 6 — parameters.
- **Task:** Phases 0–5 all done. Scheduling fires jobs through generated
  systemd (user) `.timer`/`.service` units (decision #27), not an in-process
  cron loop — the supervisor renders/reloads them
  (`internal/scheduling`, `internal/systemdunit`,
  `cmd/supervisor/schedule.go`), and the api process's schedule CRUD stays a
  plain Postgres write. A generated unit's `ExecStart` calls
  `descendence schedule trigger <id>`, which hits
  `POST /api/v1/schedules/{id}/trigger` (the first endpoint enforcing a
  scope) — that endpoint applies the overlap policy and then reuses
  `createJobRun`, the same run-creation logic `CreateJobRunHandler` was
  refactored to share.
- **Next action:** 6.1 — extend the manifest with the parameter contract
  (name, type, required, default); see ARCHITECTURE.md §4.7 for the four
  deliberately-separate concerns (contract / introspection / form / binding
  map) before starting.
- **Blocked on:** nothing
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

- [ ] **6.1** Extend the manifest with the parameter contract (name, type, required,
      default).
- [ ] **6.2** Validate submitted params against the contract server-side; reject
      clearly on mismatch.
- [ ] **6.3** Mount params as JSON at `/run/job/params.json`.
- [ ] **6.4** Write shims: Bash, Python, PowerShell.
- [ ] **6.5** Store params on the run record for reproducibility — **redact anything
      marked secret**.
- [ ] **6.6** Wire Podman secrets as a parameter type (`type=mount`).
- [ ] **6.7** *Optional:* prototype PowerShell AST introspection (open question in
      ARCHITECTURE.md §8). Best-effort only — never a runtime dependency.

**Exit check:** a parameter value containing `"; rm -rf /; #` is passed through
literally and harmlessly.

---

## Phase 7 — Web UI

Deliberately vague; scope it properly when you get here, and re-read ARCHITECTURE.md
§4.11 first.

- [ ] **7.1** Vite + TypeScript project under `web/`.
- [ ] **7.2** Generate the TS client from `openapi.yaml`.
- [ ] **7.3** Session cookie auth (OIDC comes later; a login form against a local
      account is fine first).
- [ ] **7.4** Embed the build with `//go:embed`, served same-origin.
- [ ] **7.5** Read-only views first: run list, run detail, live logs via `EventSource`.
- [ ] **7.6** Trigger runs.
- [ ] **7.7** Job and runtime management.
- [ ] **7.8** Form builder — the largest single piece. Consider shipping YAML editing
      with a rendered preview before building drag-and-drop.

---

## Learning notes

Things worth reading *when you reach them*, not upfront. Learning in the abstract
before you have a problem tends not to stick.

| Topic | When | Where |
|---|---|---|