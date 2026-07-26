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

- **Phase:** 0 — Foundations (not started)
- **Task:** —
- **Next action:** Install Go and Podman, confirm the rootless Podman socket responds
- **Blocked on:** nothing
- **Notes:** Architecture agreed. Nothing built yet.

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
- [ ] **0.7** Pick and set up a migration tool (`goose` or `golang-migrate`). Write
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

- [ ] **1.1** Migration: `principals` and `runs` tables. Use the sketch in
      ARCHITECTURE.md §5, trimmed to what Phase 1 needs.
- [ ] **1.2** Set up `pgx` (Postgres driver) and `sqlc` (generates typed Go from your
      SQL). Write one query, generate, call it.
- [~] **1.3** Write `api/openapi.yaml` with exactly three operations:
      `POST /api/v1/runs`, `GET /api/v1/runs/{id}`, `GET /api/v1/runs`.
      Not started — spec currently only covers `/` and `/healthz`. Those two are
      done but were never part of 1.3's three operations
- [!] **1.4** Write routing and handlers directly in `internal/api`, no chi, no codegen, for
      learning value. `internal/api` with `apiServer` struct + constructor +
      handler methods exists and serves `/` and `/healthz`
- [ ] **1.5** Auth middleware: read `Authorization: Bearer`, hash it, look up the
      principal, reject with 401 if unknown. One bootstrap token, inserted by a
      migration or a `make seed` target.

> **Why auth now, when it's a single-user tool?** Because every handler needs a
> `principal_id` to stamp onto records. Adding the concept later means touching every
> handler and backfilling every table. It's ~30 lines today.

- [ ] **1.6** Implement `POST /api/v1/runs`: validate, insert a row with state
      `queued`, return `202 Accepted` with the ID and a `Location` header.
- [ ] **1.7** Implement `GET /api/v1/runs/{id}`.
- [ ] **1.8** Honour `Idempotency-Key`: unique index on it; a repeat returns the
      original run rather than creating a second.

### 1b. Podman client

- [ ] **1.9** `internal/podman`: an HTTP client over the Unix socket
      (`http.Client` with a custom `DialContext`). First call: `/libpod/info`.
- [ ] **1.10** Implement create / start / wait / remove container.
      **Always label the container with `run_id`.**
- [ ] **1.11** Build argv as a **`[]string`, never a shell string.** Write a test that
      passes `; rm -rf /` as an argument and asserts it is treated as literal text.

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

- [ ] **1.18** Generate the API client from the same `openapi.yaml`.
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