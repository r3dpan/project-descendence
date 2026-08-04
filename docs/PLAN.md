# Build Plan

**Companion document:** ARCHITECTURE.md (the *what* and *why*; this file is the *when*)
**Last updated:** 2026-07-22

---

## How to use this document

This project will be worked on in bursts with weeks of nothing in between. That is
fine, but it means the plan must survive being forgotten. Two rules:

1. **Update the "Current position" block below at the end of every session.** Even
   one line. This is the single most valuable thing in the file.
2. **Append to the session log at the bottom.** What you did, what broke, what you
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

- **Phase:** 1 — Vertical slice
- **Task:** Phase 1a done. 1.9 done. 1.10 done. 1.11 done - Phase 1b's
  `internal/podman` client work is now done. 1.12 next.
- **Next action:** 1.12 — supervisor claim loop: poll `queued` runs with
  `SELECT ... FOR UPDATE SKIP LOCKED`, mark `running`, record `started_at`.
  First code in `cmd/supervisor`, currently empty.
- **Blocked on:** nothing
- **Notes:** Phase 0, 1a done (see prior entries / commits for detail). Phase
  1b's client side is done too: `internal/podman` —
  `Client`/`Info`/`CreateContainer`/`StartContainer`/`WaitContainer`/
  `RemoveContainer`, every container unconditionally `run_id`-labelled
  (`CreateContainerParams.RunID` required, not optional). Two integration
  tests in `containers_test.go` (`go test ./internal/podman/...`, skip
  cleanly without `PODMAN_SOCKET`): full lifecycle with a real exit code, and
  1.11's argv-injection test — a single argv element `"; rm -rf /"` makes the
  OCI runtime fail to exec a file literally named that, proving the string
  was never shell-split. `go vet ./...` clean, no leaked containers after
  either test. `cmd/supervisor` and `cmd/cli` are still empty — 1.12 onward is
  what actually makes a `queued` row turn into a running container.

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

- [ ] **1.12** Claim loop: poll for `queued` runs using
      `SELECT ... FOR UPDATE SKIP LOCKED`, mark `running`, record `started_at`.
- [ ] **1.13** Execute: create the container, start it, wait, record `exit_code`,
      set the terminal state, `finished_at`. Remove the container.
- [ ] **1.14** Implement all six states: `queued`, `running`, `succeeded`, `failed`,
      `cancelled`, `lost`.
- [ ] **1.15** **Reconciler.** On startup, list containers filtered by the `run_id`
      label and compare against runs in non-terminal states. Adopt what's still
      alive; mark the rest `lost`.
- [ ] **1.16** Advisory lock so a second supervisor refuses to start (or waits).
- [ ] **1.17** Timeouts: a per-run maximum duration, enforced with `context.Context`;
      on expiry, kill the container and record `failed` with a reason.

### 1d. CLI

- [ ] **1.18** Hand write the API client from the same `openapi.yaml`.
- [ ] **1.19** `cli run` — creates a run and polls until terminal, printing state.
- [ ] **1.20** `cli runs list`, `cli runs get <id>`.
- [ ] **1.21** Config: server URL and token from env vars or `~/.config/<name>/config`.

### 1e. Prove it

Do these deliberately — they are the point of the phase.

- [ ] **1.22** Kill the supervisor mid-run, restart it. The reconciler behaves correctly.
- [ ] **1.23** Kill the API mid-poll. Runs continue unaffected.
- [ ] **1.24** Submit 20 runs at once. All complete, none run twice.
- [ ] **1.25** No leaked containers: `podman ps -a` is clean after everything finishes.

**Exit check:** all of 1.22–1.25 pass. Do not move on until they do.

---

## Phase 2 — Logs

**Goal:** see what a script printed, live and afterwards.
**Done when:** `cli run --follow` streams output as it happens, and closing/reopening
resumes without gaps.

- [ ] **2.1** Attach to container output in the supervisor; write to a per-run file
      with sequence numbers.
- [ ] **2.2** Record log metadata (seq, stream, timestamp) in Postgres; keep bodies
      in files. Decide and write down the retention policy (open question in
      ARCHITECTURE.md §8).
- [ ] **2.3** Fan-out: one attach per run, N subscribers. Bounded channels; drop slow
      consumers rather than blocking the writer.
- [ ] **2.4** `GET /api/v1/runs/{id}/logs` — historical, paginated.
- [ ] **2.5** Same endpoint with `Accept: text/event-stream` → SSE. Emit `id:`,
      `event:`, `data:` with a blank line between messages; one `data:` line per
      output line. Call `Flush()` after every message.
      **Constrained by `WriteTimeout`.** `cmd/api/main.go` sets a server-wide
      `WriteTimeout` (30s). A streaming response is cut off at that deadline, so
      SSE will not work without an override. Use `http.NewResponseController(w)`
      and `SetWriteDeadline(time.Time{})` inside this handler only — a zero
      `time.Time` disables the deadline for that one response. Do not solve this
      by removing the server-wide timeout.
- [ ] **2.6** Honour `Last-Event-ID` on reconnect: replay from that sequence number.
- [ ] **2.7** Exit the stream goroutine on `r.Context().Done()` — otherwise every
      closed client leaks a goroutine.
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

---

## Session log

Append one entry per session. Newest at the bottom.

```
### YYYY-MM-DD
Worked on:
Completed:
Broken / unresolved:
Next action:
Notes to future me:
```

### 2026-07-22
Worked on: architecture and planning only.
Completed: ARCHITECTURE.md and PLAN.md agreed.
Broken / unresolved: nothing yet.
Next action: Phase 0, task 0.1.
Notes to future me: the whole design rests on the rootless Podman socket working
(task 0.2). Verify that before writing any Go.

### 2026-07-26
Worked on: `openapi.yaml` for `/` and `/healthz` (JSON responses, versioned info);
`internal/api` package — `apiServer` struct, `NewAPIServer` constructor, response
types `serverInfo`/`serverHealth`, handler methods; `cmd/api/main.go` wired to use it.
Completed: 0.6 (health endpoint, now via `internal/api`, not inline in `main.go`).
Decided: hand-write all routing/handlers, no chi, no oapi-codegen — reverses the
codegen half of decision #11. Needs a decision-log entry in ARCHITECTURE.md.
Broken / unresolved: run endpoints (1.3's three operations) not yet in the spec.
Postgres (0.4) and Podman socket (0.2) still not started — nothing past route
skeletons is unblockable until then.
Next action: write `Run`/`RunCreate`/`Problem` schemas and the three run
operations into `openapi.yaml`, verify in Bruno, then stub the Go handlers as 501s.
Notes to future me: 0.2/0.4/0.7 are the real next Phase-0 blockers — everything in
1.5–1.8 needs Postgres. Don't let the API skeleton progress create the illusion
that Phase 0 is done.

### 2026-07-27
Worked on: 0.2 (rootless Podman socket), 0.3 (lingering), 0.4 (Postgres via Quadlet).
Completed:
  - 0.3: `loginctl enable-linger` confirmed (`Linger=yes`).
  - 0.4: `postgres.container` + `postgres-data.volume` Quadlet units running from
    `~/.config/containers/systemd/` (symlinked to a custom directory for easier
    editing). Postgres 18.4 on `127.0.0.1:5432`, connection verified via DBeaver
    (no psql installed in this WSL2 instance — updated task wording to match).
  - 0.2: rootless `podman.socket` enabled and verified via
    `curl --unix-socket $XDG_RUNTIME_DIR/podman/podman.sock http://d/v5.0.0/libpod/info`.
  - 0.5 — created the remaining git repo skeleton.
Broken / unresolved: nothing currently open.
Next action: 0.7 - Pick and set up a migration tool
Notes to future me:
  - Postgres 18+ images changed the expected mount point from
    `/var/lib/postgresql/data` to `/var/lib/postgresql` (version-specific subdir
    underneath, for `pg_upgrade --link` support). The Quadlet `Volume=` line
    reflects this now — don't revert it if you see the old convention elsewhere.
  - `~/.config/containers/systemd/` is a symlink to a custom directory. If Quadlet
    ever "loses" units again, check that the symlink still resolves before
    debugging anything else.
  - WSL2 quirk: lingering keeps the systemd user manager alive across logout, but
    doesn't stop the whole WSL2 instance from shutting down on its own idle timer.
    Deliberately left `.wslconfig` idle-timeout settings alone since I'm always
    attached via terminal or VS Code during dev sessions and don't mind it shutting
    down otherwise. Revisit this if the supervisor ever needs to run unattended
    (Phase 5, scheduling).
  - `sudo systemctl --user ...` fails with a DBus/XDG_RUNTIME_DIR error — always use
    plain `systemctl --user`, no sudo, for anything user-scoped.

### 2026-07-29
Worked on: API server hardening; migration tooling (0.7).
Completed:
  - Routing: `GET /{$}` and `GET /healthz` — Go 1.22+ patterns. Unmatched paths now
    404, wrong methods 405 with `Allow`, instead of the old `/` catch-all answering
    everything with server info.
  - Explicit `http.Server` with ReadHeaderTimeout 5s / ReadTimeout 15s /
    WriteTimeout 30s / IdleTimeout 120s, replacing `http.ListenAndServe`.
  - `internal/api`: keyed composite literals; `writeJSON` helper holding the single
    `Encode` error check; both handlers reduced to build-value-and-call.
  - 0.7: goose chosen and installed, `.env` config, migration 001 (throwaway
    `testing` table), up and down both verified.
  - Phase 0 exit check passed — container + volume destroyed, recreated, migrated.
Broken / unresolved: nothing open.
Next action: 1.1 — migration 002 for `principals` and `runs`.
Notes to future me:
  - goose keys on the numeric prefix, not the filename. Renaming an applied
    migration is safe; editing its SQL is not — goose skips it as done.
  - `goose down` unwinds in reverse version order. You cannot remove a lower
    migration without rolling back everything above it.
  - Migrations run in a transaction by default, so a syntax error leaves nothing
    behind. `-- +goose NO TRANSACTION` gives that up (needed for
    CREATE INDEX CONCURRENTLY).
  - `identity ALWAYS` vs `BY DEFAULT` — decide per table in 1.1.

### 2026-08-04
Worked on: repo/documentation audit at the start of the session; PLAN.md accuracy;
1.1 (apply + commit migration); 1.2 (`pgx` + `sqlc`); 1.5 (auth middleware);
1.3 (openapi spec for the three run operations); 1.6 (`POST /api/v1/runs`);
1.7 (`GET /api/v1/runs/{id}`); 1.8 (`Idempotency-Key`); `GET /api/v1/runs`
(list) — completing 1.3's three operations and closing out Phase 1a; 1.9
(`internal/podman` client, opening Phase 1b); 1.10 (container lifecycle);
1.11 (argv injection test) — closing out `internal/podman`'s client side.
Completed:
  - 1.1: found `migrations/00001_create_database.sql` already written (full
    ARCHITECTURE.md §5 schema, all eight tables) but untracked and unapplied.
    Ran `goose up`, verified all eight tables in `descendent_db` via
    `podman exec postgres psql`, committed the migration.
  - 1.2: installed `sqlc` (`go install .../sqlc/cmd/sqlc@latest`, v1.31.1), added
    `github.com/jackc/pgx/v5/pgxpool` (`go get` + `go mod tidy`). `sqlc.yaml` at
    repo root points `schema: migrations`, `queries: internal/store/queries`, and
    generates into `internal/store` (package `store`, `sql_package: pgx/v5`). One
    query so far: `Ping` (`SELECT 1`) in `internal/store/queries/health.sql`.
    `cmd/api/main.go` now reads `DATABASE_URL`, opens a `pgxpool.Pool`, builds
    `store.New(pool)`, and passes it into `api.NewAPIServer`. `HealthHandler`
    calls `Ping` and returns `503` + `databaseUp: false` if it fails.
    `api/openapi.yaml`'s `/healthz` schema updated to match (`databaseUp` field,
    documented `503` response). Verified live: `go run ./cmd/api` +
    `curl /healthz` → `{"healthStatus":"Healthy","databaseUp":true}`.
  - 1.5: `internal/store/queries/principals.sql` — `GetPrincipalByTokenHash`,
    `CreateTokenPrincipal`. `internal/api/auth.go` — `RequireAuth` middleware:
    extracts `Authorization: Bearer <token>`, SHA-256s it, looks up
    `principals.token_hash` (query already filters `kind='token'`,
    `revoked_at IS NULL`, unexpired), stamps the principal onto the request
    context, or writes a `problem+json` 401 (new `writeProblem` helper in
    `api.go`, first use of RFC 9457 in this codebase). `cmd/seed/main.go` mints
    one bootstrap principal: `crypto/rand` → 32 bytes → `sra_live_<hex>`,
    SHA-256 stored, plaintext printed once, scopes `read`/`run`/`admin`. Added
    `GET /api/v1/whoami` (behind `RequireAuth`) purely to prove the path — it
    isn't one of 1.3's three run operations. Verified live: no token → 401,
    wrong token → 401, real bootstrap token → 200 with the principal's
    id/name/kind/scopes. `openapi.yaml` gained a `bearerAuth` security scheme,
    `Principal`/`Problem` schemas, and the `/api/v1/whoami` path.
  - 1.3: added `RunCreate` (`imageRef`, `argv` min 1 item, `timeoutSeconds`
    default 3600), `Run` (mirrors the `runs` table, nullable fields as
    `type: [x, "null"]` per OpenAPI 3.1 / JSON Schema 2020-12 — no `nullable:`
    keyword, that's a 3.0-ism), and `RunList` (`items` + `nextCursor`).
    `POST /api/v1/runs`: `Idempotency-Key` header (new reusable
    `components.parameters.IdempotencyKey`), `202` + `Location`, `400`/`401`.
    `GET /api/v1/runs`: `cursor`/`limit` query params, keyset not offset (table
    grows forever, ARCHITECTURE.md §4.9). `GET /api/v1/runs/{id}`: `200`/`404`.
    Spec-only — verified the YAML parses and every `$ref` resolves (ad hoc
    Python check, no linter installed); no handlers behind any of these three
    yet.
  - 1.6: `internal/store/queries/runs.sql` (`CreateRun`) + `internal/api/runs.go`
    (`CreateRunHandler`, `runCreateRequest`/`runResponse`, `toRunResponse`
    converting sqlc's `pgtype`-heavy `store.Run` into the wire shape).
    Registered `POST /api/v1/runs` behind `RequireAuth` — `principal_id` comes
    from the auth middleware's context, not the request body. Validates
    `imageRef` non-empty, `argv` non-empty, `timeoutSeconds` positive when
    given (defaults to 3600 otherwise). `Idempotency-Key` intentionally not
    read yet - that's 1.8. Verified live end to end (`go run ./cmd/api` +
    `curl`): valid create → `202`, `Location: /api/v1/runs/{id}`, row visible
    in Postgres with `state='queued'`; empty `argv` → `400`; no token → `401`;
    an `argv` element of `"; rm -rf /"` round-tripped as one literal array
    element in the DB, never interpreted as shell.
  - 1.7: `internal/store/queries/runs.sql` (`GetRun`) + `GetRunHandler`.
    Malformed and unknown ids both map to `404` (spec doesn't document a `400`
    for this route). Deliberately not scoped to the caller's principal — full
    RBAC is deferred (ARCHITECTURE.md §7), this is a single-user tool for now.
    Verified live: real id → `200` with the full run body; unknown id → `404`;
    non-numeric id (`not-a-number`) → `404`; no token → `401`.
  - 1.8: `CreateRun` gained an `ON CONFLICT (principal_id, idempotency_key)
    DO NOTHING` + `RETURNING`; `pgx.ErrNoRows` from that now means "conflict,
    not error" and `CreateRunHandler` falls back to the new
    `GetRunByIdempotencyKey` to return the original run. No header → `NULL` →
    never conflicts → always inserts, same code path either way. Verified
    live: same key twice (second request had a different body) → both `202`s
    return the *first* run's data at the *first* run's id; a different key, or
    no key, both create genuinely new rows. Confirmed the id sequence still
    skips a value on a conflict-skipped insert (expected Postgres behavior,
    not a bug — `nextval()` isn't transactional with `DO NOTHING`).
  - `GET /api/v1/runs`: `internal/store/queries/runs.sql` (`ListRuns`, using
    `sqlc.narg`/`sqlc.arg` for the nullable cursor columns + row limit) +
    `ListRunsHandler`. Cursor is `base64(RFC3339Nano(queuedAt) + "|" + id)` -
    opaque to clients, encodes the exact seek position. Fetches `limit+1` rows
    to detect a next page without a separate `COUNT`; `nextCursor` is `null`
    once fewer than `limit+1` rows come back. Added an undocumented-until-now
    `400` to the spec for malformed cursor / out-of-range limit (`GetRun`
    chose to fold malformed input into `404` instead — different call here
    since a bad cursor silently returning page 1 would be more confusing than
    an honest error). Verified live: created 5 runs, paged through with
    `limit=2` across 3 pages in correct newest-first order with no gaps or
    repeats, `nextCursor: null` on the last page; default (no params) returned
    all 5 in one page; `limit=0`/`limit=abc` → `400`; malformed cursor → `400`;
    no token → `401`. This closes out Phase 1a - all three 1.3 operations and
    1.4's handlers are now implemented.
  - 1.9: `internal/podman/podman.go` — `NewClient(socketPath)` builds an
    `http.Client` whose `Transport.DialContext` always dials the given Unix
    socket regardless of the request URL's host (used a placeholder
    `http://d/...`, matching the `curl --unix-socket` convention from the
    Phase 0 session log). `Info()` calls `GET /v5.0.0/libpod/info`, decoded
    into a minimal struct (just `host.arch`/`host.os`/`version.*` - not
    modeling Podman's full info schema, only what's used so far). New
    required env var `PODMAN_SOCKET` (`.env`/`.env.sample`), defaults to this
    machine's `/run/user/1000/podman/podman.sock`. Wired into `/healthz` as a
    `podmanUp` field, same pattern as `Ping` for the database in 1.2 - not a
    new task, just reusing the established "prove the client against
    `/healthz`" approach. Verified live: real socket → `podmanUp: true`,
    `200`; `PODMAN_SOCKET` pointed at a nonexistent path → `podmanUp: false`,
    `503`. Went with plain `net/http` over the official
    `github.com/containers/podman/v5/pkg/bindings`, per the already-recorded
    ARCHITECTURE.md §6 decision #3 (bindings pull in native build deps for
    ~15 endpoints this project actually needs).
  - 1.10: probed the real socket with `curl --unix-socket` first (create,
    start, wait, inspect, remove, plus two error cases) to pin down exact
    wire shapes before writing any Go - worth doing again for any new libpod
    endpoint, since e.g. `/wait`'s plain-text response was not obvious from
    memory alone. `internal/podman/containers.go`:
    `CreateContainer`/`StartContainer`/`WaitContainer`/`RemoveContainer`.
    Refactored `Info`'s request/error handling into shared `do()`/
    `checkStatus()` in `podman.go` (libpod error body:
    `{"cause","message","response"}`). `CreateContainerParams.RunID` is
    required, always becomes the `run_id` label - can't be skipped by a
    future caller, unlike an optional `Labels` map would allow. Wrote
    `containers_test.go` as a real integration test against the live socket
    (skips cleanly if `PODMAN_SOCKET` unset/unreachable, doesn't fail CI or a
    podman-less environment) — full lifecycle against Alpine, `sh -c "exit
    42"`, confirmed exit code, confirmed the label via a manual `curl`
    inspect, confirmed `podman ps -a` shows nothing left behind.
  - 1.11: probed the real socket first again - created a container whose
    entire `command` was `["; rm -rf /"]` and checked what actually happens.
    `start` returns HTTP `500` immediately (not the `204` a normal start
    gives) with body `{"cause":"OCI runtime attempted to invoke a command
    that was not found","message":"...exec: \"; rm -rf /\": stat ; rm -rf /:
    no such file or directory...","response":500}` - i.e. Podman tried to
    exec a file literally named `; rm -rf /` and failed, which is exactly the
    proof needed that the string was one atomic argv token, never shell-split
    on `;`. Also confirmed a plain (non-force) `DELETE` still removes a
    container that never successfully started. Wrote
    `TestCreateContainerArgvNeverShellInterpreted` asserting on both of those:
    `StartContainer` returns an error, and the error text contains both the
    literal `"; rm -rf /"` and `"no such file"`. Refactored the "connect or
    skip" boilerplate shared with 1.10's test into `newTestClient(t)`. Ran
    `go vet ./...` (clean) and `go test ./...` (both podman tests pass, no
    leaked containers afterward) - this closes out `internal/podman`'s
    client-side work; nothing execution-related is left to build before the
    supervisor.
Broken / unresolved: nothing.
Next action: 1.12 — supervisor claim loop: poll for `queued` runs using
`SELECT ... FOR UPDATE SKIP LOCKED`, mark `running`, record `started_at`.
First code in `cmd/supervisor`, which is currently an empty directory. 1.13
(execute: create/start/wait/record exit_code/remove) follows immediately -
`internal/podman` already has every primitive 1.13 needs.
Notes to future me:
  - 1.1 went wider than planned (all eight tables, not just `principals`/`runs`)
    — future-phase tables are pre-created but commented as skeletons, so this
    isn't scope creep so much as writing the whole sketch down once.
  - `sqlc generate` (from `$HOME/go/bin`, not yet on PATH in this shell) must be
    re-run any time `internal/store/queries/*.sql` or the migration schema
    changes — generated files are committed like any other source, not built in
    CI here.
  - `DATABASE_URL` is a new env var, separate from `GOOSE_DBSTRING` — goose and
    the app now each source their own connection string from `.env`, on purpose
    (goose is a separate CLI tool, not app config).
  - `cmd/seed` is a one-shot: `principals.name` is unique, so running it twice
    against the same database fails on the second `bootstrap` insert (expected,
    not a bug) — revoke/rotate has no endpoint yet, so replacing the token means
    an `UPDATE`/`DELETE` by hand for now.
  - `go run ./cmd/api &` in a shell: killing the parent PID it prints doesn't
    kill the compiled child binary `go run` execs — kill the actual `api`
    process (check `ss -ltnp` on :8080) or the port stays bound.