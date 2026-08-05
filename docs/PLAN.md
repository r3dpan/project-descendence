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
2. **Append to the session log in HISTORY.md the bottom.** What you did, what broke, what you
   were about to do next. Future-you is a different person with no memory.

### Status markers

Update the marker on each task as it moves:

- `[ ]` not started
- `[~]` in progress
- `[x]` done
- `[!]` blocked or deferred — always add a note saying why

### Reading order after a long break

1. "Current position" block, below
2. The last 2–3 session log entries
3. The phase you're in
4. ARCHITECTURE.md section 6 (decision log) if a design choice feels arbitrary

---

## Current position

> **Update this block every session.**

- **Phase:** 2 — Logs
- **Task:** Phase 1 complete (1.1–1.25). **Phase 2: 2.1–2.7 done.** Output
  is captured, indexed, retained for 30 days, served as JSON pages *and*
  as a live SSE stream that resumes cleanly after a disconnect. What is
  left is 2.8 (cancel) and 2.9 (CLI `--follow`), then the phase exit check.
- **Next action:** 2.8 — `POST /api/v1/runs/{id}/cancel`. Read the phase's
  warning first: get cancellation and context propagation right *here*,
  because scheduling and parameters land on top of it. The debt from 1.14
  is still exactly as described — `cancelled` is defined, constrained,
  rendered and tested everywhere and has no producer — so this is the
  transition plus stopping the container, not plumbing a new state. Note
  the shape of the problem: the API accepts the cancel, but only the
  supervisor can stop a container, and **the two never talk** (§3). The
  same `LISTEN`/`NOTIFY` channel 2.3 built is the obvious carrier, with
  the same caveat that notifications are lossy — so the supervisor must
  also notice a cancellation request by polling, or a cancel will
  occasionally do nothing at all.
- **Blocked on:** nothing
- **Notes:** Phase 2 so far:

  - **Log bodies are files, the index is Postgres** (ARCHITECTURE.md §4.1).
    `RUN_LOG_DIR` must be set for *both* the api and the supervisor, and
    must be the same directory — the supervisor is the sole writer, the
    API only reads. That shared filesystem is a third channel the §3
    diagram originally lacked, and it is what pins the two processes to one
    host; it is the first assumption that breaks under multi-node
    (decision #19).
  - **The invariant to not break:** flush the file, *then* write the index
    row, *then* notify. The index row is what tells a reader those bytes
    exist, so any other order publishes an offset pointing past the end of
    the file. Stated in `runlog.Flush`, in `runlogs.sql`, and in
    `capturePass`.
  - **Captured output is not to be trusted without a completeness check.**
    Two separate things were silently dropping lines — journald's rate
    limiter (decision #20) and libpod's follower cutting off at container
    exit (decision #21) — and *neither produced an error anywhere*. Both
    are fixed, but the shape is worth remembering: in this pipeline,
    failure looks like less output, not like a failure.
  - **Once a run is terminal, its log index is complete.** The supervisor
    drains the capture before writing the terminal state, deliberately, so
    a stream can end when the run ends without racing the last few lines.
    Do not reorder that.
  - **Notifications are lossy on purpose.** They carry watermarks, not log
    text, so a dropped or missed one costs latency and nothing else. Every
    consumer must therefore poll on a slow timer as well; a consumer that
    trusts notifications alone will hang the first time the listener
    reconnects.
  - **Recapture is always from scratch**, never a resume at an offset —
    both for the reconciler adopting a run and for decision #21's second
    pass. It is safe because libpod replays the same bytes in the same
    order, so the same lines come back with the same sequence numbers and
    offsets. Only the capture timestamp changes.
  - Runs within one supervisor process still execute strictly one at a
    time. Unchanged by Phase 2, and still the most likely thing to bite in
    real use (see 1e's note on reconciliation blocking the claim loop).

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
      Written as `migrations/00001_create_database.sql`, scope grew to the full
      §5 sketch (all eight tables) instead of just these two. Applied via
      `goose up` and committed.
- [x] **1.2** Set up `pgx` (Postgres driver) and `sqlc` (generates typed Go from your
      SQL). Write one query, generate, call it.
      `sqlc.yaml` + `internal/store/queries/health.sql` (`Ping`) generate into
      `internal/store/`. Wired into `cmd/api/main.go` via `pgxpool`, called from
      `HealthHandler` — `/healthz` now reports real `databaseUp` status.
- [x] **1.3** Write `api/openapi.yaml` with exactly three operations:
      `POST /api/v1/runs`, `GET /api/v1/runs/{id}`, `GET /api/v1/runs`.
      All three specced: `RunCreate`/`Run`/`RunList` schemas, `Idempotency-Key`
      request header (component parameter, enforcement is 1.8), `202` +
      `Location` on create, keyset `cursor`/`limit` query params on list (no
      offset pagination — see ARCHITECTURE.md §4.9), `401`/`404`/`400` via the
      existing `Problem` schema. Spec only — no handlers behind these three yet,
      that's 1.6–1.8.
- [x] **1.4** Write routing and handlers directly in `internal/api`, no chi, no codegen, for
      learning value. `internal/api` (`api.go`, `auth.go`, `runs.go`) with
      `APIServer` struct + constructor + handler methods for `/`, `/healthz`,
      `/api/v1/whoami`, and all three run operations (`POST`/`GET`
      `/api/v1/runs`, `GET /api/v1/runs/{id}`) — routed in `cmd/api/main.go`
      via the stdlib Go 1.22+ mux.
- [x] **1.5** Auth middleware: read `Authorization: Bearer`, hash it, look up the
      principal, reject with 401 if unknown. One bootstrap token, inserted by a
      migration or a `make seed` target.
      `internal/api/auth.go` — `RequireAuth` middleware, SHA-256 over the raw
      token, `problem+json` 401s. Went with a `cmd/seed` Go command rather than
      a Go migration for the bootstrap token (decision #16 anticipated either) —
      simpler than teaching goose's Go-migration mode for one row. Token format
      `sra_live_<64 hex>` per ARCHITECTURE.md §4.10. Proved via
      `GET /api/v1/whoami`, which didn't exist before this task.

> **Why auth now, when it's a single-user tool?** Because every handler needs a
> `principal_id` to stamp onto records. Adding the concept later means touching every
> handler and backfilling every table. It's ~30 lines today.

- [x] **1.6** Implement `POST /api/v1/runs`: validate, insert a row with state
      `queued`, return `202 Accepted` with the ID and a `Location` header.
      `internal/store/queries/runs.sql` (`CreateRun`) + `internal/api/runs.go`
      (`CreateRunHandler`, registered behind `RequireAuth`). Validates
      `imageRef` non-empty, `argv` non-empty, `timeoutSeconds` positive
      (defaults to 3600); `principal_id` comes from the auth middleware's
      context, not the body. `Idempotency-Key` not read yet — deliberately
      deferred to 1.8. Verified live: valid create → `202` + `Location` +
      `queued` row in Postgres; empty `argv` → `400`; no token → `401`;
      an `argv` value shaped like a shell injection (`"; rm -rf /"`) stored
      as one literal array element, never interpreted.
- [x] **1.7** Implement `GET /api/v1/runs/{id}`.
      `internal/store/queries/runs.sql` (`GetRun`) + `GetRunHandler`. Malformed
      or unknown id both return `404` (spec only documents `200`/`401`/`404`
      for this route, so a `400` for malformed ids was left out on purpose).
      Not principal-scoped — any authenticated caller can read any run; full
      RBAC is deferred (ARCHITECTURE.md §7) and this is a single-user tool for
      now. Verified live: existing id → `200`, unknown id → `404`, non-numeric
      id → `404`, no token → `401`.
- [x] **1.8** Honour `Idempotency-Key`: unique index on it; a repeat returns the
      original run rather than creating a second.
      `CreateRun` uses `ON CONFLICT (principal_id, idempotency_key) DO NOTHING`
      + `RETURNING`; a skipped insert surfaces as `pgx.ErrNoRows`, which
      `CreateRunHandler` treats as "fetch and return the original" via the new
      `GetRunByIdempotencyKey` query, rather than an error. No header at all →
      `idempotency_key` stays `NULL`, which Postgres never treats as
      conflicting, so unkeyed requests always insert. Verified live: same key
      twice (different body the second time) → both `202`s point at the same
      run id and return the *original* body; a different key or no key at all
      → distinct new runs. Note: the id sequence still advances on a skipped
      insert (`ON CONFLICT` doesn't roll back `nextval()`) — gaps in `runs.id`
      are expected, not a bug.

### 1b. Podman client

- [x] **1.9** `internal/podman`: an HTTP client over the Unix socket
      (`http.Client` with a custom `DialContext`). First call: `/libpod/info`.
      `podman.Client`, socket path from `PODMAN_SOCKET` (new required env var,
      `.env`/`.env.sample`). Wired into `/healthz` as `podmanUp`, same pattern
      as `Ping` for the database. Verified live against the real socket and a
      broken one.
- [x] **1.10** Implement create / start / wait / remove container.
      **Always label the container with `run_id`.**
      `internal/podman/containers.go`: `CreateContainer` (`POST
      /libpod/containers/create`, `201`), `StartContainer` (`POST .../start`,
      `204`), `WaitContainer` (`POST .../wait` — response is plain text, not
      JSON, unlike every other libpod endpoint used so far; parses the exit
      code), `RemoveContainer` (`DELETE /libpod/containers/{id}`). Shared
      `do()`/`checkStatus()` helpers moved into `podman.go` so `Info` (1.9)
      and the container calls share request/error handling; libpod's error
      body shape is `{"cause","message","response"}`, confirmed by probing
      the real socket with `curl` before writing any Go. `RunID` on
      `CreateContainerParams` is required (not an optional label) so the
      `run_id` label can't be skipped by a future caller. Verified live via
      `go test ./internal/podman/...`: full create/start/wait/remove cycle
      against real Alpine, exit code round-tripped correctly, label confirmed
      present via a manual `curl` inspect, no container left behind
      afterward (`podman ps -a`).
- [x] **1.11** Build argv as a **`[]string`, never a shell string.** Write a test that
      passes `; rm -rf /` as an argument and asserts it is treated as literal text.
      Already true end to end since 1.6/1.10 (`runs.argv` is `text[]`,
      `CreateContainerParams.Command`/libpod's `command` field are both
      `[]string`) - this task added the explicit proof.
      `TestCreateContainerArgvNeverShellInterpreted` in `containers_test.go`:
      a container whose sole argv element is `"; rm -rf /"` fails to start
      with an OCI "exec: not found" error naming that exact literal string,
      proving it was looked up as one atomic token rather than shell-split on
      `;`. Confirmed by probing the real socket with `curl` first (both the
      failure shape and that a plain `DELETE` still cleans up a
      never-started container).

### 1c. Supervisor

- [x] **1.12** Claim loop: poll for `queued` runs using
      `SELECT ... FOR UPDATE SKIP LOCKED`, mark `running`, record `started_at`.
      `ClaimNextQueuedRun` (`internal/store/queries/runs.sql`) is a single
      statement: a `FOR UPDATE SKIP LOCKED` CTE feeding a data-modifying
      `UPDATE ... RETURNING`, so claim-and-transition is atomic - no
      select-then-update gap. `cmd/supervisor/main.go`: 1s-tick polling loop,
      drains every queued run per tick, `signal.NotifyContext` for clean
      SIGINT/SIGTERM shutdown. Verified live with two supervisor processes
      running concurrently against 6 queued runs - all 6 claimed exactly
      once between them (5/1 split), zero duplicates, zero left behind.
- [x] **1.13** Execute: create the container, start it, wait, record `exit_code`,
      set the terminal state, `finished_at`. Remove the container.
      `cmd/supervisor/execute.go`: `executeRun` + `finishRun` (writes via the
      new `FinishRun` query) + `removeContainer` (fresh `context.Background()`
      so a cancelled supervisor still cleans up a container whose run just
      finished). `exitCode == 0` → `succeeded`; nonzero → `failed` with
      `failureReason = "exit code N"`; a create/start/wait error also →
      `failed`, with the error itself as `failureReason` and no `exit_code`.
      Verified live: real success, real nonzero exit, and a nonexistent image
      all reached the correct terminal state with correct `exit_code`/
      `container_id`/`failure_reason`, zero leaked containers.
- [x] **1.14** Implement all six states: `queued`, `running`, `succeeded`, `failed`,
      `cancelled`, `lost`. New `internal/store/states.go` (hand-written,
      lives beside the generated code) is the authoritative Go list, with
      `IsTerminal` and the state machine documented as a diagram naming who
      performs each transition; the supervisor's string literals are gone.
      **`FinishRun` now guards on `state IN ('queued','running')` and is
      `:execrows`** — a terminal state is final, so a slow reconciler can no
      longer rewrite a real `succeeded` as `lost`; zero rows is logged as
      "already terminal" rather than silently clobbering. `cancelled` is
      defined, constrained, rendered and tested everywhere but has no
      producer: task 2.8 owns cancellation end to end, and the plan is
      emphatic about getting it right there, so building half of it here
      was deliberately declined. Drift between the three copies of the
      state list (Go, the `runs_state_check` constraint, `openapi.yaml`'s
      enum) is now caught by tests.
- [x] **1.15** **Reconciler.** On startup, list containers filtered by the `run_id`
      label and compare against runs in non-terminal states. Adopt what's still
      alive; mark the rest `lost`.
      New `podman.ListContainersByRunIDLabel` (`all=true` + a `label` filter,
      confirmed via `curl` first that libpod's `State` field uses `"created"`/
      `"running"`/`"stopped"`, not Docker's `"exited"`) and
      `ListNonTerminalRuns` (`state IN ('queued','running')`). Taken out of
      order before 1.14 - see "Current position". Three-way classification
      per non-terminal run: no matching container → `lost`; container found
      but `State == "created"` (crashed between create and start, so there's
      no outcome to adopt) → `lost` + remove the stale container; anything
      else (running, or already exited but never recorded) → adopt via the
      same `waitFinishAndRemove` tail `executeRun` (1.13) already uses -
      `WaitContainer` returns immediately if the container already exited, so
      "still running" and "finished but unrecorded" need no special-casing.
      Queued runs are skipped entirely - they never have a container in this
      design. Runs synchronously before the claim loop starts, so a
      long-running adopted run currently delays new queued runs from being
      claimed; noted as a known simplification, not a bug, since nothing
      today runs multiple runs concurrently within one supervisor anyway.
      Verified live with four simulated crash scenarios (state hand-edited in
      Postgres + containers created directly via `curl` to fake each case):
      a live/recently-exited container → adopted, correct terminal state; no
      container → `lost`; a created-but-never-started container → `lost` +
      container removed; an already-exited-but-unrecorded container →
      adopted, correct exit code. `podman ps -a` empty afterward in every
      case.
- [x] **1.16** Advisory lock so a second supervisor refuses to start (or waits).
      Chose "refuses to start" over "waits" - fails fast with a clear log
      line and non-zero exit, matching how a systemd restart policy would
      want to see it, rather than a process silently hanging. `pg_try_advisory_lock`
      on a fixed key (`8817001`), held on a connection acquired from the pool
      and never returned to it for the process's lifetime (session-level
      lock semantics require that - a pooled connection reused by unrelated
      queries would break it). Verified live: second supervisor refuses
      immediately while the first runs; lock is free again immediately after
      graceful shutdown.
- [x] **1.17** Timeouts: a per-run maximum duration, enforced with `context.Context`;
      on expiry, kill the container and record `failed` with a reason.
      Deadline computed from `run.StartedAt + run.TimeoutSeconds` (survives
      a supervisor restart correctly for adopted runs - no fresh clock).
      New `podman.KillContainer`. Distinguishes "timed out" from "supervisor
      is shutting down" via `errors.Is(waitCtx.Err(), context.DeadlineExceeded)`
      - shutdown leaves the run `running` for the reconciler instead of
      marking it failed. Verified live both from a fresh claim and from
      reconciler adoption of an already-expired run.

### 1d. CLI

- [x] **1.18** Hand write the API client from the same `openapi.yaml`.
      `internal/client`: `client.go` (transport, `APIError` + `ErrNotFound`/
      `ErrUnauthorized` sentinels via a custom `Is`, `Info`, `Health`,
      `WhoAmI`) and `runs.go` (`Run`/`RunList` types, state constants,
      `Run.IsTerminal`, `CreateRun` with `Idempotency-Key`, `GetRun`,
      `ListRuns`, `PollRun`). Nullable schema fields are pointers so a
      `exitCode` of 0 is distinguishable from "hasn't finished". `/healthz`
      is the one endpoint whose 503 carries a real body rather than a
      problem document, handled with an explicit `alsoOK` status list.
      Integration tests in `client_test.go` skip cleanly unless
      `DESCENDENCE_URL`/`DESCENDENCE_TOKEN` are set (same pattern as
      `internal/podman`); all pass against a live API + supervisor.
- [x] **1.19** `cli run` — creates a run and polls until terminal, printing state.
      `cmd/cli`: stdlib `flag` dispatch, bubbletea rendering. Two watch
      paths chosen on `isTTY(os.Stdout)` — a live spinner + state view when
      interactive, one line per *state change* plus a summary when piped.
      Exits with the run's own exit code (1 when a failure produced none),
      so it composes in a shell. `-detach` prints just the id; `-timeout`
      and `-key` map to the API's timeout and `Idempotency-Key`. Ctrl-C
      stops the watch, never the run (no cancel endpoint until Phase 2) and
      says so. **Found and fixed a real pre-existing bug while verifying:**
      `internal/podman`'s blanket `http.Client.Timeout` (10s) also applied
      to the long-polling `/wait` call, so every run over 10s was marked
      `failed` with an infrastructure error *and leaked its container*.
      Split into `httpClient` / `longPollClient`; regression test added.
- [x] **1.20** `cli runs list`, `cli runs get <id>`.
      `runs get` reuses `renderRunSummary`, so a run looks identical
      whether you watched it, listed it or fetched it. `runs list` has the
      same TTY/non-TTY split as 1.19: a browsable `bubbles/table` that
      loads further pages as the cursor reaches the bottom (so the opaque
      keyset cursor is never shown to the user) with enter to open a run in
      full; `tabwriter`-aligned rows plus `-all` to follow every page when
      piped. Columns flex with terminal width, argv favoured over image ref.
- [x] **1.21** Config: server URL and token from env vars or `~/.config/<name>/config`.
      `~/.config/descendence/config` (or `$DESCENDENCE_CONFIG`), hand-rolled
      `key = value` parser - no TOML dependency. Environment wins over file
      **per value**, so overriding just the URL keeps the stored token.
      Unknown keys and malformed lines are errors with line numbers, not
      silent no-ops. Warns when the file (which holds a token) is readable
      by anyone else. New `descendence config` prints the resolved values,
      where each came from, and the file path - the token only ever as its
      trailing 8 characters, matching the server's `token_hint`.

### 1e. Prove it

Do these deliberately — they are the point of the phase.

- [x] **1.22** Kill the supervisor mid-run, restart it. The reconciler behaves correctly.
      Run as five scenarios, one per reconciler branch:
      **A** SIGKILL, container already exited → adopted, real outcome
      recorded (not `lost`). **B** SIGKILL, container genuinely still
      running → adopted live, waited the full 40.5s, `succeeded`, and the
      timeout clock was *not* reset. **C** SIGKILL + container removed by
      hand → `lost`. **D** container created but never started → `lost`
      and the stale container removed. **E** graceful SIGTERM → run left
      `running` and the container untouched, adopted on restart. The
      advisory lock was reacquired immediately after every SIGKILL, so a
      hard crash does not lock the supervisor out of restarting.
- [x] **1.23** Kill the API mid-poll. Runs continue unaffected.
      `kill -9` on the API while the CLI was polling: the CLI failed fast
      with a legible `connection refused` and exit 1 rather than hanging,
      the supervisor never noticed, and the run completed and was recorded
      normally *with no API process running at all*. Restarting the API
      showed the finished run intact. Decision #6 (separate processes
      sharing only Postgres) actually paying out.
- [x] **1.24** Submit 20 runs at once. All complete, none run twice.
      20 concurrent submissions, each with a distinct expected exit code:
      all 20 reached a terminal state with exactly the exit code its argv
      asked for. "None run twice" checked three ways – each run appears
      exactly once in the supervisor's claim log, the 20 runs hold 20
      distinct `container_id`s, and no `container_id` is shared by any two
      runs anywhere in the table. Also confirmed the mechanism that
      guarantees this across processes: a second supervisor refuses to
      start on the advisory lock.
- [x] **1.25** No leaked containers: `podman ps -a` is clean after everything finishes.
      Clean after all of the above – only the persistent `postgres`
      container, nothing carrying a `run_id` label, zero non-terminal runs,
      and no terminal run missing a `finished_at`.

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
      `internal/podman.FollowContainerLogs` (libpod's multiplexed frame
      format, probed rather than assumed: 8-byte header, 0x01 stdout /
      0x02 stderr, big-endian length) plus `internal/runlog`, which splits
      frames into lines itself — frame boundaries are *not* line
      boundaries — carrying a partial line per stream. On `longPollClient`,
      not the 10s one: the same bug shape as 1.19 would have silently
      truncated the logs of every run over 10s. Sequence numbers are
      arrival order, not emission order (stdout and stderr are buffered
      separately inside the container); documented rather than papered
      over. The supervisor waits for capture to drain before removing a
      container, since `WaitContainer` returns while frames may still be
      unread. New config: `RUN_LOG_DIR`.
- [x] **2.2** Record log metadata (seq, stream, timestamp) in Postgres; keep bodies
      in files. Decide and write down the retention policy (open question in
      ARCHITECTURE.md §8).
      `run_logs` written with COPY, coalescing whatever is already queued
      into one batch. The ordering rule is load-bearing: flush the file,
      *then* publish the index rows, or a row points past EOF.
      **Retention resolved — ARCHITECTURE.md decision #18:** run records
      forever, run *output* for 30 days, swept hourly by the supervisor
      (it holds the advisory lock, so there is exactly one sweeper).
      Migration 00002 adds `runs.logs_pruned_at`, which is what lets the
      API tell "printed nothing" from "output deleted". Per-run size cap
      deliberately not built.
- [x] **2.3** Fan-out: one attach per run, N subscribers. Bounded channels; drop slow
      consumers rather than blocking the writer.
      **The plan and ARCHITECTURE.md §4.2 both had this in the wrong
      process.** The subscribers are HTTP clients and the supervisor serves
      no HTTP, so fan-out lives in the *API* (`internal/logstream`); the
      supervisor only emits a `NOTIFY` watermark. §4.2 corrected in place,
      recorded as decision #19. Events are watermarks ("run 42 has output
      through seq 900"), never log text — so dropping under load is safe,
      a missed notification costs latency rather than correctness, and
      payloads stay far inside NOTIFY's 8000-byte limit. Subscribers still
      poll on a slow timer as the safety net.
- [x] **2.4** `GET /api/v1/runs/{id}/logs` — historical, paginated.
      Paginated by sequence number, not an opaque cursor: a log line's
      position is public — it is the same number `Last-Event-ID` carries in
      2.6 — so hiding it here and exposing it there would be incoherent.
      Index from Postgres, bodies from the file, opened once per page.
      Returns `runState` so a polling client needs no second request.
      A pruned run is **410 Gone**, checked *before* reading: pruning
      deletes the index rows too, so a pruned run is otherwise
      indistinguishable from one that printed nothing.
- [x] **2.5** Same endpoint with `Accept: text/event-stream` → SSE. Emit `id:`,
      `event:`, `data:` with a blank line between messages; one `data:` line per
      output line. Call `Flush()` after every message.
      **Constrained by `WriteTimeout`.** `cmd/api/main.go` sets a server-wide
      `WriteTimeout` (30s). A streaming response is cut off at that deadline, so
      SSE will not work without an override. Use `http.NewResponseController(w)`
      and `SetWriteDeadline(time.Time{})` inside this handler only — a zero
      `time.Time` disables the deadline for that one response. Do not solve this
      by removing the server-wide timeout.
      `internal/api/sse.go` (the wire format) + `streamRunLogs` in `logs.go`.
      Two event types: `log` (id = seq, data = the same object the JSON path
      returns) and `state` (no id — not a resumable position). A `state`
      event carrying a terminal state is the stream's *defined* ending;
      ending any other way is the client's cue to reconnect.
      **Deviated from the `SetWriteDeadline(time.Time{})` instruction above,
      deliberately.** A cleared deadline swaps one bug for a worse one: a
      client that stops reading without closing leaves the handler blocked
      in `Write` forever, holding the goroutine and subscription 2.7 exists
      to release (TCP keepalive notices in *hours*). The deadline is
      re-armed before every write instead — same 30s, applied per write
      rather than per response, so a stream lives as long as it likes and a
      stalled write still dies on schedule.
      Error paths are checked *before* the stream headers go out, because
      after them the only way to report a 404 is to hang up.
      Verification found two real defects that had nothing to do with SSE;
      see decisions #20 and #21, and the session log.
- [x] **2.6** Honour `Last-Event-ID` on reconnect: replay from that sequence number.
      The header **overrides `?after`**, which matters more than it sounds:
      an `EventSource` reconnects to the *same URL* by itself and cannot
      rewrite the query string, so the `?after` it sends back is whatever
      the stream was originally opened with. Honouring that would replay
      the whole run on every reconnect, forever.
      Resuming loses and repeats nothing because sequence numbers are
      dense and monotonic within a run and `after` is exclusive - true
      even across a recapture (decision #21), which reproduces the same
      lines under the same numbers. A malformed header is a 400 rather
      than a silent restart from the beginning: answering "resume where I
      left off" with the entire run is the one thing the client did not
      ask for.
      Verified with nine forced disconnects mid-run: 150 lines received,
      150 in the index, zero duplicates, dense 1..150, correct order.
- [x] **2.7** Exit the stream goroutine on `r.Context().Done()` — otherwise every
      closed client leaks a goroutine.
      The handler was written this way in 2.5; this task is the *proof*,
      and writing it changed two things. First, `internal/api` had no
      tests at all, so there was nowhere for a guard like this to live —
      `logs_test.go` starts that.
      Second, and worth remembering: **a test that only asserts the
      subscription is released does not test this line.** Deleting the
      `Done` case entirely still passed, because every read a stream makes
      carries the request context, so the handler unwinds anyway on its
      next safety-net poll. What `Done` buys is *promptness*, so the test
      asserts the handler returns in less than one poll interval — that
      assertion does fail without it (2.003s).
      Also verified on the real HTTP path, since the tests drive the
      handler directly: 25 live SSE clients against a 60s run, all killed
      at once, zero `streamRunLogs` goroutines left in the process
      afterwards (`SIGQUIT` dump).
- [ ] **2.8** `POST /api/v1/runs/{id}/cancel` — propagate cancellation, stop the
      container, record `cancelled`.
- [ ] **2.9** CLI `--follow`.

> Get cancellation and context propagation right **here**. It gets much harder once
> scheduling and parameters are layered on.

**Exit check:** a 60-second script streams live; disconnect and reconnect mid-run and
no lines are lost or duplicated; cancel works within a second or two.

---

## Phase 3 — Jobs and git

**Goal:** named, reusable jobs backed by version-controlled scripts.
**Done when:** `cli job run backup-db` works, and the run records the commit SHA.

- [ ] **3.1** Migration: `repos`, `jobs`. Add `job_id` and `commit_sha` to `runs`.
- [ ] **3.2** Create and manage a bare git repo on disk by shelling out to `git`.
- [ ] **3.3** Define the sidecar manifest format (`<name>.job.yaml`) — start minimal:
      name, script path, runtime, description.
- [ ] **3.4** Scan a repo, parse manifests, sync into the `jobs` table.
- [ ] **3.5** `POST /api/v1/jobs/{id}/runs` — resolve the current commit SHA, copy the
      script into the container, record the SHA on the run.
- [ ] **3.6** Job CRUD endpoints + `cli job list/get/run`.
- [ ] **3.7** Upload a script through the API → commit to the repo with the calling
      principal as author.

**Exit check:** change a script, run it, and the new run's `commit_sha` differs from
the previous one. You can check out any past SHA and see exactly what ran.

---

## Phase 4 — Runtimes

**Goal:** scripts run in an environment with their dependencies.
**Done when:** a Python job using `requests`, and a PowerShell job using a PSGallery
module, both run successfully.

- [ ] **4.1** Migration: `runtimes`. Add `runtime_id` and `image_digest` to `runs`.
- [ ] **4.2** Choose base images (resolve the Alpine-vs-Debian open question — note
      PowerShell compatibility pushes toward Debian).
- [ ] **4.3** Containerfile template + renderer (see ARCHITECTURE.md §4.4).
- [ ] **4.4** Build via the Podman API; tag with a hash of the inputs so identical
      definitions dedupe.
- [ ] **4.5** Builds are async: `POST /runtimes/{id}/build` returns 202; status is
      polled. Reuse the run machinery if it fits.
- [ ] **4.6** Resolve tag → digest after build; runs pin the **digest**.
- [ ] **4.7** Prune policy for old images. Decide it, write it down, implement it.
- [ ] **4.8** CLI `runtime list/create/build`.

**Exit check:** both target jobs run; rebuilding a runtime does not change what an
already-scheduled run executes.

---

## Phase 5 — Scheduling

**Goal:** jobs run unattended.
**Done when:** a job runs every 5 minutes for an hour without intervention, including
across a supervisor restart.

- [ ] **5.1** Resolve the open question: in-process cron vs. generated systemd timers.
      Record the decision in ARCHITECTURE.md §6.
- [ ] **5.2** Migration: `schedules`.
- [ ] **5.3** Implement the scheduler in the supervisor, under the advisory lock.
- [ ] **5.4** Handle missed windows: after downtime, do you catch up or skip? Decide
      explicitly — the default should probably be skip.
- [ ] **5.5** Timezone and DST handling. Store the timezone; do not assume UTC.
- [ ] **5.6** Overlap policy: what happens when a run is still going and the next is
      due? (skip / queue / run concurrently — make it per-job.)
- [ ] **5.7** Schedule CRUD + CLI.

**Exit check:** restart the supervisor mid-schedule; no duplicate and no missed runs.

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
| Go basics, `context.Context`, goroutines, channels | Before Phase 1 | A Tour of Go, then Effective Go |
| `net/http`, custom `DialContext` | Task 1.9 | Go stdlib docs |
| `SELECT ... FOR UPDATE SKIP LOCKED` | Task 1.12 | Postgres docs on row locking |
| Advisory locks | Task 1.16 | Postgres docs |
| Podman REST API | Task 1.9 | docs.podman.io API reference |
| Server-Sent Events wire format | Task 2.5 | MDN, "Using server-sent events" |
| OpenAPI 3 basics | Task 1.3 | The spec's own guide; keep it minimal at first |
| Quadlet | Task 0.4 | `man podman-systemd.unit` |

### Things beginners commonly get wrong here

- **Ignoring returned errors in Go.** Handle every one, even if handling means
  logging and returning. Silent failures in a background supervisor are brutal to debug.
- **Long-lived database connections held across a whole job run.** Take a connection,
  do the query, release it. Jobs run for minutes; connections must not.
- **Forgetting `defer` on cleanup.** Container removal, file handles, transactions.
- **Building SQL by string concatenation.** Use parameterised queries — `sqlc` makes
  this the default path.
- **Trying to make it perfect before it works.** Ugly-but-working, then refactor.