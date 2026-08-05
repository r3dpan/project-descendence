# Session log

Append one entry per session. Newest at the bottom.

```
### YYYY-MM-DD
Worked on:
Completed:
Broken / unresolved:
Next action:
Notes to future me:
```

## 2026-07-22
Worked on: architecture and planning only.
Completed: ARCHITECTURE.md and PLAN.md agreed.
Broken / unresolved: nothing yet.
Next action: Phase 0, task 0.1.
Notes to future me: the whole design rests on the rootless Podman socket working
(task 0.2). Verify that before writing any Go.

## 2026-07-26
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

## 2026-07-27
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

## 2026-07-29
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

## 2026-08-04
Worked on: repo/documentation audit at the start of the session; PLAN.md accuracy;
1.1 (apply + commit migration); 1.2 (`pgx` + `sqlc`); 1.5 (auth middleware);
1.3 (openapi spec for the three run operations); 1.6 (`POST /api/v1/runs`);
1.7 (`GET /api/v1/runs/{id}`); 1.8 (`Idempotency-Key`); `GET /api/v1/runs`
(list) — completing 1.3's three operations and closing out Phase 1a; 1.9
(`internal/podman` client, opening Phase 1b); 1.10 (container lifecycle);
1.11 (argv injection test) — closing out `internal/podman`'s client side;
1.12 (supervisor claim loop, opening Phase 1c); 1.13 (execute) — completes
the CLI-less end of the vertical slice: submit a run over HTTP, it actually
executes in a container and lands on a real terminal state; 1.15 (reconciler)
— skipped ahead of 1.14 deliberately, at the user's direction; 1.16 (advisory
lock) — also skipped ahead of 1.14, at the user's direction; 1.17 (timeouts)
— closes out Phase 1c except for 1.14.
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
  - 1.12: `ClaimNextQueuedRun` — a `FOR UPDATE SKIP LOCKED` CTE feeding a
    data-modifying `UPDATE ... RETURNING` in one statement, so the "pick a
    row" and "mark it running" steps are atomic; zero rows back is just an
    empty queue (`pgx.ErrNoRows`), not an error. `cmd/supervisor/main.go`:
    `runClaimLoop` ticks every second, `claimAllQueued` drains the queue each
    tick by calling the claim query until it errors, `signal.NotifyContext`
    on SIGINT/SIGTERM for clean shutdown. Verified live: created 6 queued
    runs via the API, ran two `cmd/supervisor` processes at once for a few
    seconds - all 6 runs ended up `running` with `started_at` set, split 5/1
    between the two processes with no run claimed twice and none left
    `queued`. This is the concurrency property the whole design leans on
    (ARCHITECTURE.md §4.1, "exactly one worker grab a row without blocking
    others") actually holding under two real OS processes, not just reasoned
    about. Execution isn't wired in yet - a claimed run currently stays
    `running` forever with no container behind it; that's 1.13.
  - 1.13: new `FinishRun` query (plain `UPDATE ... WHERE id = $1`, `:exec`).
    `cmd/supervisor/execute.go`: `executeRun` runs
    create→start→wait→finish→remove in sequence; every failure branch
    (create error, start error, wait error, and the normal nonzero-exit path)
    still calls `finishRun` with `state="failed"` before returning, so there
    is no code path that leaves a run `running` without an infrastructure
    error to explain it. `removeContainer` deliberately uses
    `context.Background()` instead of the loop's cancellable `ctx` - a run
    that finishes right as the supervisor is shutting down should still get
    its container cleaned up rather than leaking it because `ctx` already
    cancelled. `finishRun`'s nil/empty checks (`exitCode != nil`,
    `containerID != ""`, `failureReason != ""`) build the right `pgtype.Int4`/
    `pgtype.Text` `Valid` flags - a nil `exitCode` correctly leaves the column
    `NULL` for the "never got a container" failure case. Verified live: three
    real runs through the full HTTP → Postgres → supervisor → Podman path -
    `exit 0` → `succeeded`/`exit_code=0`; `exit 17` → `failed`/`exit_code=17`/
    `failure_reason="exit code 17"`; a nonexistent image → `failed`/
    `exit_code=NULL`/`container_id=NULL`/`failure_reason` containing Podman's
    actual `"no such image"` error. `podman ps -a` showed nothing left behind
    in any of the three cases.
  - 1.15: asked to skip ahead of 1.14 to the reconciler. New
    `podman.ListContainersByRunIDLabel` - probed the real
    `GET /libpod/containers/json?all=true&filters={"label":["run_id"]}`
    endpoint first (as with every other libpod call this session) and found
    libpod's `State` field says `"created"`/`"running"`/`"stopped"`, not
    Docker's `"exited"` - would have gotten the never-started-vs-exited check
    wrong by guessing. New `ListNonTerminalRuns` query. `reconcile()` in
    `cmd/supervisor/reconcile.go`, called once at startup before
    `runClaimLoop`: builds a `run_id → ContainerSummary` map from the
    container list, then for every non-terminal run (skipping `queued` ones -
    they never have a container yet by construction) - no container found →
    `lost`; container `State == "created"` → `lost` + remove the stale
    container (it never ran, nothing to adopt); anything else → adopt via
    `waitFinishAndRemove`, the same tail `executeRun` already uses since
    `WaitContainer` resolves immediately for an already-exited container, no
    separate "already finished" branch needed. Verified live with four
    simulated crashes (state hand-edited in Postgres, containers created
    directly via `curl` to fake each scenario, since there's no automated way
    yet to actually crash the supervisor mid-run): live container → adopted,
    `succeeded`, `exit_code=0`; no container → `lost`; created-but-never-
    started → `lost` + container removed; already-exited-but-unrecorded
    (`exit 9`) → adopted, `failed`, `exit_code=9`. `podman ps -a` empty
    afterward every time. Chose to run reconciliation synchronously before
    the claim loop starts rather than adopting concurrently in goroutines -
    simpler, and correctness-wise sufficient for now since nothing else in
    this codebase runs multiple runs concurrently within one supervisor
    process either; flagged as revisitable if a long-running adopted run
    ever needs to not block newly queued ones from being claimed.
  - 1.16: new `internal/store/queries/supervisor.sql` -
    `TryAdvisoryLock`/`AdvisoryUnlock`, both `sqlc.arg(lock_key)::bigint` (a
    bare `$1::bigint` gets sqlc a working but uglily-named `dollar_1`
    parameter - naming it via `sqlc.arg` is worth doing by default, not just
    when it happens to break). `cmd/supervisor/lock.go`:
    `acquireSingletonLock` pulls one dedicated connection via `pool.Acquire`
    and never releases it back to the pool for the process's lifetime -
    necessary because `pg_try_advisory_lock` is session-scoped, and a pooled
    connection could otherwise get silently reused by unrelated queries out
    from under the lock. Chose fail-fast ("refuses to start") over
    ("waits") - non-zero exit with a clear message suits a systemd restart
    policy better than a hung process; `pg_advisory_lock` (the blocking
    variant) was the road not taken here, not because it's wrong, just a
    design call in `PLAN.md`'s explicit either/or. Fixed lock key `8817001`
    - arbitrary, but its exact value is now load-bearing (see the comment
    above the constant on why it must never casually change). Verified live:
    two supervisors at once → second refuses immediately, non-zero exit,
    first unaffected; lock free again immediately after graceful shutdown
    (confirmed by starting a third right after with zero delay). Note for
    later: this breaks the "run two `cmd/supervisor` processes at once"
    verification trick 1.12 and 1.15 both used to prove `SKIP LOCKED` - that
    property would now need two goroutines calling `ClaimNextQueuedRun`
    within one process (or a deliberate lock bypass for the test) to
    re-verify, not two OS processes.
  - 1.17: deadline computed once, in `waitFinishAndRemove`, as
    `run.StartedAt.Time.Add(time.Duration(run.TimeoutSeconds) * time.Second)`
    - deliberately from `StartedAt`, not `time.Now()` at the point of
    calling `WaitContainer`, so a reconciler-adopted run (1.15) is bounded by
    what's actually left of its original budget, not a fresh full timeout.
    `context.WithDeadline(ctx, deadline)` wraps just the `WaitContainer`
    call. On error, three-way branch on `waitCtx.Err()`:
    `errors.Is(..., context.DeadlineExceeded)` → genuine timeout →
    `handleTimeout` (new `podman.KillContainer` via `POST .../kill`,
    confirmed via `curl` first that libpod's default signal is SIGKILL and
    the endpoint returns `204`; then a fresh `WaitContainer` just to confirm
    it actually stopped before removing, ignoring that exit code since it's
    meaningless - the run didn't finish on its own); `waitCtx.Err() != nil`
    but not that specific error → the *parent* context was cancelled first,
    i.e. supervisor shutdown, not a timeout → deliberately touch nothing and
    leave the run `running` for the reconciler; neither → the pre-existing
    generic `WaitContainer` failure path, unchanged. All of `handleTimeout`'s
    Podman/DB calls use a fresh `context.Background()`, same reasoning as
    `removeContainer` already established in 1.13 - the context that just
    expired obviously can't be reused. Verified live twice: a `sleep 30`
    with `timeoutSeconds: 3` was killed at ~3s (not 30s), `failed`,
    `exit_code` `NULL`, `failure_reason="exceeded timeout of 3s"`, no leaked
    container, while an unrelated normal run in the same batch still
    succeeded on its own generous timeout; separately, hand-editing a run's
    `started_at` to an hour in the past with a 10s timeout and a live
    container, then starting the supervisor fresh, killed it immediately on
    reconcile - the elapsed-time-survives-a-restart property, proven, not
    just asserted in a comment.
Broken / unresolved: nothing. 1.14 (all six states) was intentionally
skipped three times, not forgotten - see "Current position".
Next action: 1.14 - implement all six run states. Likely mostly a
verification pass given `queued`/`running`/`succeeded`/`failed`/`lost` all
already exist and are exercised live; `cancelled` genuinely can't be reached
until Phase 2's cancel endpoint exists. After 1.14, Phase 1c is fully done and
1d (hand-written CLI client, tasks 1.18-1.21) is next.
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

## 2026-08-05
Worked on: Phase 1d (the CLI). 1.18 (hand-written API client); 1.19
(`cli run`) — plus a genuine `internal/podman` bug that 1.19's verification
surfaced, see below; 1.20 (`cli runs list` / `runs get`); 1.21 (config file
+ precedence) — **Phase 1d complete**; 1.14 (all six run states) — the last
open item in 1c; then Phase 1e in full (1.22-1.25), which passes.
**PHASE 1 IS COMPLETE.**
Decision up front, at the user's explicit direction: **the CLI is built on
the Charm stack** — `bubbletea` for anything interactive, with `bubbles` and
`lipgloss` as needed. That is a real dependency choice for a project that has
otherwise hand-rolled everything (decisions #3 and #15), so it is recorded as
decision #17 in ARCHITECTURE.md rather than left implicit in the code.
Completed:
  - 1.18: `internal/client`, written from `api/openapi.yaml` by hand, split
    into `client.go` (transport + the non-run endpoints) and `runs.go`.
    Deliberate choices worth remembering: every schema field *not* in the
    spec's `required` list is a pointer (`*int32`, `*time.Time`), because
    `exitCode: 0` means success and must never be confused with "hasn't
    finished yet" — the single most likely bug in a client like this.
    Errors are one concrete `*APIError` carrying the RFC 9457 problem body,
    with a custom `Is` mapping 401/404 onto `ErrUnauthorized`/`ErrNotFound`
    so callers use `errors.Is` and never type-assert on a status code.
    `/healthz` needed a small escape hatch (`requestOptions.alsoOK`) since
    it is the one endpoint that answers 503 with a genuine body rather than
    a problem document. `PollRun` is included here rather than in the CLI
    because the non-TTY path of 1.19 wants exactly a blocking poll loop;
    the bubbletea path will drive its own ticks instead.
    Verified live against a real API + supervisor: create→poll→`succeeded`
    with `exitCode` 0, idempotency replay returning the same run id, keyset
    pagination advancing across pages, and both sentinels firing. Tests skip
    cleanly when `DESCENDENCE_URL`/`DESCENDENCE_TOKEN` are unset, matching
    `internal/podman`'s pattern, so `go test ./...` still passes bare.
  - 1.19: `cmd/cli` — `main.go` (dispatch), `config.go`, `run.go`,
    `watch.go` (the bubbletea model), `style.go`. Design decisions worth
    keeping: **two watch paths**, chosen on `isTTY(os.Stdout)` — the TUI
    when interactive, one line per *state change* (not per poll) plus the
    same summary block when piped, because a CLI that emits spinners and
    cursor movement into a pipe is unusable in a script. The model owns its
    own polling (one `tea.Tick` command per observation, the next scheduled
    only when the previous message arrives, so polls can never pile up)
    rather than reusing `client.PollRun` — bubbletea's loop must never
    block. `PollRun` still gets used, by the non-TTY path, which is exactly
    what it was written for. **The CLI exits with the run's own exit code**
    (1 when a failure produced none, 130 on Ctrl-C), so it composes in a
    shell like a local command. Ctrl-C stops the *watch*, never the run —
    there is no cancel endpoint until Phase 2 and claiming otherwise would
    be a lie; the TUI catches it as a keypress (raw mode swallows the
    signal) and the plain path as a cancelled context, and both say the run
    continues. Verified live: `echo` (exit 0), `exit 42` → CLI exits 42,
    `-timeout 3` on a `sleep 30` → red `failed` + "exceeded timeout of 3s",
    argv containing `; rm -rf /` and `$(whoami)` passed through literally,
    `-detach` printing a bare id, `-key` replaying to the same run id, both
    error sentinels, and SIGINT mid-watch leaving the run to finish on its
    own (it reached `succeeded` afterwards, no leaked container). The TUI
    itself was verified under a real pty via `script`; its *logic* is unit
    tested in `watch_test.go` by calling `Update`/`View` directly, which is
    both faster and stricter than driving a terminal.
  - 1.19 (bug found, not planned): `internal/podman`'s `http.Client` had a
    blanket `Timeout: 10s`, which also applied to `WaitContainer` — a
    long-polling call that by design blocks for the container's entire
    lifetime. Every run over 10 seconds was therefore marked `failed` with
    `waiting for container: ... Client.Timeout exceeded`, *and* leaked its
    container (the supervisor then tried to remove one that was still
    running). Invisible until now because every previous verification used
    runs shorter than 10s — 1.17's timeout tests used a 3s run timeout on a
    `sleep 30`, so the run deadline always fired first. Fixed by splitting
    into `httpClient` (bounded, `requestTimeout`) and `longPollClient`
    (unbounded — the wait is bounded by the caller's context, which the
    supervisor already derives from the run's own timeout in 1.17).
    `TestWaitContainerOutlivesTheRequestTimeout` sleeps deliberately past
    the boundary; confirmed it fails (and reproduces the leak) when the
    timeout is put back.
  - 1.20: `cmd/cli/runs.go` (dispatch, `get`, the plain list) and
    `list.go` (the `bubbles/table` model). `runs get` reuses
    `renderRunSummary` from 1.19 — a run looks identical whether you
    watched it, listed it or fetched it, which matters more than it
    sounds: it means there is one place to change when the Run schema
    grows. Same TTY/non-TTY split as 1.19. The interactive list does
    **infinite scroll**: reaching the last row fetches the next page and
    appends, so the opaque keyset cursor never has to be surfaced to the
    user, which is the entire point of it being opaque. Enter opens the
    highlighted run in full and exits; quitting leaves the table in the
    scrollback rather than wiping it. The piped path uses stdlib
    `text/tabwriter` and takes `-all` to follow every page, otherwise
    printing one page and saying so on *stderr* (so `| wc -l` still counts
    only rows).
    Deliberately **not** colour-coding the state cell in the table:
    `bubbles/table` styles cells uniformly and paints the selected row over
    the top, so per-cell ANSI fights the selection highlight and risks
    breaking width calculations. Colour carries meaning in the detail view
    instead, where nothing competes with it.
    Verified live: `runs get` for a real/missing/non-numeric id, the piped
    list against the whole session's history, `-limit 5` + `-all` (21 lines
    = header + 20 runs, matching the unpaged list), a server-side
    `-limit 9999` rejection surfacing as a problem detail, and the table
    rendered under a pty at both 80 and 150 columns.
  - 1.21: `cmd/cli/config.go`. `~/.config/descendence/config` via
    `os.UserConfigDir` (which already handles `XDG_CONFIG_HOME`), or
    `$DESCENDENCE_CONFIG` to point elsewhere - which is also what makes the
    tests hermetic. Format is `key = value` with `#`/`;` comments, parsed by
    hand: two keys do not justify a TOML dependency in a project that
    hand-rolled its Podman client. Precedence is environment over file
    **per value**, not all-or-nothing, so `DESCENDENCE_URL=... descendence
    ...` overrides one setting for one invocation while the stored token
    still applies. A missing file is fine (env-only is a legitimate setup);
    a file that exists but is malformed, or has an unknown key, is an error
    with a line number - a typo'd `tokn` that silently does nothing is
    exactly the kind of thing that costs an hour. Warns (does not refuse)
    when the file is group/world readable, since it holds a token.
    New `descendence config` answers "why is it talking to the wrong
    server": resolved values, the source of each, and the file path, with
    the token shown only as its trailing 8 characters - which happens to
    match the `token_hint` the server stores, so the two can be compared by
    eye. Verified end to end with **no environment variables at all**:
    `whoami`, a full `run`, and `runs list` all working from the file
    alone, plus per-value override, the permission warning, an unknown key,
    and the nothing-configured message.
  - 1.14, finally, after four deliberate deferrals — and it was *not* just
    a verification pass, which is the lesson. Three real pieces of work:
    (a) `internal/store/states.go`, hand-written beside the generated code
    (sqlc only rewrites its own outputs, so it survives `sqlc generate`),
    holding the six constants, `IsTerminal`, and a diagram of the state
    machine naming who performs each transition. The supervisor's string
    literals are gone.
    (b) **`FinishRun` had no state guard** — `WHERE id = $1` would happily
    overwrite a terminal run. Concretely: a reconciler slow to notice a run
    had finished could rewrite a real `succeeded`, exit code and all, as
    `lost`. Now `WHERE id = $1 AND state IN ('queued','running')` and
    `:execrows`, so the caller can tell "recorded" from "someone else got
    here first" and log it rather than silently clobbering.
    (c) `cancelled` deliberately left with no producer. Task 2.8 owns
    cancellation end to end and the plan is emphatic about getting it right
    there; building half of it here would have been exactly the scope drift
    this document warns against. It is defined, constrained, rendered and
    tested regardless, so 2.8 only adds a transition rather than plumbing a
    new state through four layers.
    Verified live: all six states accepted by `runs_state_check` and a
    seventh rejected; the guard proven both in raw SQL and through the
    generated Go (new DB-backed tests in `internal/store`, skipping on an
    unset `DATABASE_URL` like the other integration suites); the CLI
    rendering all six; and the reconciler marking two genuinely stranded
    runs `lost` on restart.
  - 1.14 (bug found in my own test, worth recording): the first version of
    `TestFinishRunWillNotOverwriteATerminalState` called
    `ClaimNextQueuedRun` in a loop to get its run into `running`. That
    query returns the *oldest* queued run, not yours — so under `go test
    ./...`, where packages run in parallel, it stole and abandoned runs
    belonging to `internal/client`'s tests, stranding them in `running`.
    Symptom was the client package suddenly taking 60s (its poll deadline)
    instead of 1.5s. Fixed by finishing straight from `queued`, which
    exercises the same guard without reaching outside its own row. **Any
    test that claims work from a shared queue is a test that interferes
    with every other test.**
  - Phase 1e (1.22-1.25): all four pass — see the task entries for what was
    actually exercised. Worth saying plainly: **1e was not a formality.**
    Three real defects fell out of it, none of which any amount of code
    reading had found:
    1. **`container_id` was NULL for the whole life of a run.** It was only
       written by `FinishRun`, so `GET /runs/{id}` on a *running* run
       reported `containerId: null` - no way to reach the container of a
       run still in progress, which is precisely when you want it. Fixed
       with `SetRunContainerID`, called right after `CreateContainer`; the
       CLI now shows a short container id too (and the summary's label
       column had to widen from 9 to 10 to fit "container").
    2. **Graceful shutdown logged `claim: context canceled` as an error.**
       An ordinary SIGTERM looked like a fault in the logs. The claim loop
       now suppresses it when `ctx.Err() != nil`.
    3. **`ContainerSummary.State`'s doc comment was wrong.** It claimed
       libpod reports "stopped" rather than Docker's "exited". Probing all
       three states directly showed libpod actually reports `created`,
       `running` and **`exited`**. The code was fine - the reconciler only
       special-cases "created" - but the comment would have misled the next
       person into writing `== "stopped"`.
  - Phase 1e, demonstrated (not a defect, but now proven rather than
    assumed): **reconciliation blocks the claim loop.** A run submitted
    while an adoption was in flight sat `queued` for 31 seconds until the
    adopted run finished. `reconcile.go` already documents this; 1e turned
    it into a measurement. With the default 3600s timeout, one adopted
    long run can hold the whole queue for an hour. Worth revisiting when
    concurrency arrives - it is the most likely thing to bite in real use.
Broken / unresolved: nothing. **Phase 1 is complete.**
Next action: Phase 2 (log capture and streaming), starting at 2.1.
Notes to future me:
  - `DESCENDENCE_URL` / `DESCENDENCE_TOKEN` are the CLI's env vars, chosen
    here in 1.18 (the client's tests read them) and formalised in 1.21.
    A real `~/.config/descendence/config` now exists on this machine with a
    working token, so `internal/client`'s integration tests still need the
    env vars exported but the CLI itself needs nothing.
  - `cmd/seed`'s one-shot-ness bit again: the `bootstrap` principal already
    existed, so this session's token was minted by hand as a second
    principal (`cli-dev`) with a direct `INSERT` — `sha256sum` of the token
    into `decode(...,'hex')`. Worth an actual `descendence token create`
    command eventually.
  - Driving the TUI from a test harness is not worth it. `script -qec` will
    render it (good enough for eyeballing output), but feeding it keystrokes
    doesn't work, and a hand-rolled `pty.fork()` harness hangs because
    lipgloss's `AdaptiveColor` queries the terminal for its background
    colour and nothing answers. Test `Update`/`View` directly instead —
    they're pure functions of (model, msg).
  - `bubbles/table` trap, cost an hour of confusion in 1.20: `SetHeight(h)`
    sets the *total* height and subtracts whatever the header currently
    measures — and `WithHeight` as a constructor option measures it against
    the **default** styles, before your own `SetStyles` call has run. Our
    header is two lines (titles + rule) where the default is one, so
    `New(..., WithHeight(n))` then `SetStyles(...)` leaves a permanent blank
    row. Call `SetStyles` first, then `SetHeight`.
  - The lesson from the `/wait` timeout bug generalises: **a blanket
    `http.Client.Timeout` is wrong for any long-polling endpoint.** Phase 2
    adds log streaming over the same socket, which will have exactly this
    shape — use `longPollClient` there too, not `httpClient`.

## 2026-08-05 (Phase 2, part 1)
Worked on: Phase 2 tasks 2.1-2.4 - the log pipeline from container to HTTP.
Completed:
  - 2.1: `internal/podman.FollowContainerLogs` + `internal/runlog`. Probed
    the real libpod logs endpoint before writing anything, which paid for
    itself three times: the frame format is Docker's 8-byte multiplexed
    header (0x01 stdout / 0x02 stderr, big-endian length); `follow=true`
    replays a container's *whole* output every time and returns promptly on
    an already-exited container; and an unterminated trailing fragment does
    arrive, so `printf done` is not lost. The second finding is what makes
    reconciler adoption simple - recapture from scratch, no seam to get
    wrong. Put it on `longPollClient`: the ordinary 10s client would have
    silently truncated the logs of every run over 10s, the same bug shape as
    1.19, so it gets the same deliberately-slow test.
  - 2.2: `run_logs` written with COPY, batched by draining whatever is
    already queued. Retention resolved (decision #18): runs forever, output
    30 days, swept hourly by the supervisor. Migration 00002 adds
    `logs_pruned_at`.
  - 2.3: fan-out via LISTEN/NOTIFY. See "broken/unresolved" - the plan had
    this in the wrong process.
  - 2.4: `GET /api/v1/runs/{id}/logs`, paginated by seq. Verified live:
    2000 lines over 7 pages, dense 1..2000, no gaps or repeats; a run that
    printed nothing returns an empty 200 while a pruned run returns 410.
Broken / unresolved:
  - **The plan and ARCHITECTURE.md §4.2 both put log fan-out in the
    supervisor. That is impossible** - the subscribers are HTTP clients and
    the supervisor serves no HTTP, and §3 forbids the two processes talking.
    Corrected §4.2 in place rather than leaving it wrong, and recorded the
    real design as decision #19. Worth noticing that the error survived from
    the original design document all the way to the task that implemented
    it: the diagram in §3 was right (SQL only) and the prose in §4.2 was
    wrong, and nobody reconciled them until the code forced it.
  - Nothing else open. 2.5-2.9 not started.
Next action: 2.5 (SSE). Read its `WriteTimeout` note in the task list first.
Notes to future me:
  - **A test I wrote was wrong before the code was.** The fan-out test
    asserted that a "healthy" subscriber never drops events, with its reader
    in a goroutine - and it failed, because a reader racing a tight publish
    loop simply is not scheduled often enough. The buffer is small on
    purpose; *any* subscriber momentarily behind loses events. That is fine
    (events are watermarks) but it means "healthy" has to be defined as
    "consumes each event before the next is published", which the test now
    does. If a drop-semantics test ever starts flaking, this is why.
  - `pkill -f 'go run ./cmd/supervisor'` matches the shell running it and
    kills your own session. Build to a binary and `pkill -x supervisor`.
  - Running `go test ./internal/store` against the *same* database a live
    supervisor is polling means the supervisor claims the tests' throwaway
    runs and fails them on an unpullable image. Harmless, but it is noise in
    the supervisor log and the 1.14 lesson ("any test that claims work from a
    shared queue interferes with every other test") applies in reverse too.
## 2026-08-05 (Phase 2, part 2)
Worked on: Phase 2 tasks 2.5-2.7 (SSE, resume, stream cleanup) - and two
  defects in the *capture* that verifying them uncovered, which cost more
  of the session than the three tasks did.
Completed:
  - 2.5: SSE as a second representation of `GET /runs/{id}/logs`, chosen
    by `Accept`. Two event types: `log` (id = seq) and `state` (no id -
    not a resumable position). A terminal `state` event is the stream's
    defined ending; ending any other way is the client's cue to reconnect.
  - 2.6: `Last-Event-ID` resume, overriding `?after`. Nine forced
    disconnects during a 150-line run: 150 received, 150 indexed, zero
    duplicates, dense 1..150, right order.
  - 2.7: proof, plus the first tests `internal/api` has ever had.
  - **decision #20** - pin the container log driver to `k8s-file`.
  - **decision #21** - capture in two passes: follow for liveness, re-read
    after exit for truth.
Broken / unresolved:
  - **journald was eating container output, silently.** The host default
    log driver rate-limits at 10000 messages per 30s and discards the rest
    of the window. A 20000-line script lost ~2500 lines; a second run
    started inside the same window lost *everything*; and the follow stream
    then never terminated, leaking a capture goroutine (found still blocked
    in `io.ReadFull` two minutes on, holding its podman connection). Not
    one layer reported an error, because from journald's side nothing went
    wrong. Fixed by setting the driver explicitly (decision #20).
  - **A followed log stream is not complete, even with a good driver.**
    libpod stops the follower the moment the container exits, without
    draining what the container had already written. Measured at 2643-7081
    lines missing from 20000, four runs in a row, every stream ending
    *cleanly*. Fixed by re-reading after exit and recapturing if the follow
    came up short (decision #21).
  - Nothing else open. 2.8 and 2.9 not started.
Next action: 2.8 (cancel endpoint), then 2.9 (CLI `--follow`), then the
  Phase 2 exit check. 2.8 is still owed the debt from 1.14: `cancelled` is
  defined, constrained, rendered and tested everywhere but has no producer,
  so the task is the transition and the container stop, not plumbing a new
  state. Read the phase's warning about getting cancellation and context
  propagation right *there* before starting.
Notes to future me:
  - **Both defects above had been there since 2.1 and nothing caught them,
    because 2.1's tests print about three lines.** Nothing in the pipeline
    is wrong at three lines. The regression test now prints 20000, and it
    is the reason to keep it slow-ish rather than trimming it.
  - The lesson generalises past logs: *this system's failure mode is
    silence.* Neither defect produced an error anywhere - just less output
    than the script printed. Anything downstream that reports "success"
    while holding partial data deserves a completeness check, not trust.
  - **A test asserting cleanup did not test the line it was written for.**
    Deleting `case <-r.Context().Done()` from the stream loop left both
    subscription-leak tests passing, because every read carries the request
    context so the handler unwinds on its next poll regardless. Only an
    assertion on *how long* it took (< one poll interval) failed. When
    testing a fast path that has a slow fallback, assert the speed.
  - PLAN.md told 2.5 to use `SetWriteDeadline(time.Time{})`. Deviated
    deliberately - a cleared deadline lets a client that stops reading
    without closing block the handler forever, which is exactly the leak
    2.7 exists to prevent. Re-arming the deadline before each write gives
    the same unlimited stream length with a bounded stalled write. The plan
    entry now says so.
  - `pkill -f` bit again, in a new disguise: `pgrep -f 'bin/supervisor-instr'
    | xargs kill` matched the shell running it and killed the session
    (exit 144). The rule is not about `pkill` specifically - it is that any
    `-f` pattern matches your own command line. Also `pkill -x` silently
    matches nothing when the name is over 15 characters, which is how
    `supervisor-instr` survived a `pkill -QUIT`. Capture `$!` instead.
  - Verifying against 20000-line runs leaves real containers and rows
    behind. `podman ps -a` and a non-terminal-run count are worth checking
    before calling a session done - killing the supervisor mid-run strands
    one every time (the reconciler does clean it up on restart, and did).

## 2026-08-05 (Phase 2, part 3)
Worked on: 2.8 (cancel) and 2.9 (CLI --follow), then the phase exit check.
Completed: **Phase 2 is done.** Exit check passed through the real CLI
  against the real stack - a 60s script streaming live, a follower killed
  and resumed mid-run with no gap or repeat, the API killed and restarted
  twice underneath a live follower costing 40 of 40 lines nothing, and
  cancellation landing in 1.02s three times running.
  - 2.8: `POST /runs/{id}/cancel`. Two operations behind one endpoint - the
    API cancels a *queued* run outright (the only terminal state it ever
    writes), and records a request for a *running* one, which the
    supervisor performs. Always 202. Closes the debt 1.14 deliberately left.
  - 2.9: `run -follow`, `logs [-follow]`, `cancel`. `FollowRunLogs`
    reconnects by itself, resuming from the last line delivered.
Broken / unresolved: nothing. Phase 2 complete.
Next action: Phase 3, task 3.1 (`repos` and `jobs` migration).
Notes to future me:
  - **The api→supervisor direction polls; it does not notify.** This looks
    inconsistent next to 2.3's LISTEN/NOTIFY and is the whole point:
    notifications are lossy by design, which is fine for "there is more
    output to read" and unacceptable for "stop this run". A cancel that
    takes a second beats a cancel that silently never happens. Any future
    api→supervisor command belongs in a column, not a notification.
  - **The blanket-timeout bug appeared for the third time**, in
    `internal/client` - one `http.Client` with a 30s timeout would have cut
    every `-follow` off at 30 seconds and reported a network error. Caught
    before shipping this time because the pattern is now familiar. Assume
    the fourth instance is waiting in whatever long-lived endpoint comes
    next; give it its own timeout-free client from the start.
  - The cancel watcher kills the container, and the executor checks
    `requested()` *before* reading the exit code. Get that order wrong and
    every cancelled run is recorded as `failed` with the signal's exit
    status, which defeats the reason `cancelled` is a state at all.
  - `descendence cancel` waits for the run to actually reach a terminal
    state rather than printing "cancelled" when the 202 arrives. The API
    returns 202 because the run is still running at that moment; a CLI
    reporting success there would be describing the request, not the
    outcome.
  - Ctrl-C still stops watching rather than the run - now a choice, not a
    limitation. Detaching from something is not ending it, and a `logs`
    command that killed a job on Ctrl-C would make watching dangerous.

## 2026-08-05 (interactive CLI)
Worked on: turning the CLI into a navigable application, asked for directly
  rather than as a plan task.
Completed: `cmd/cli/ui*.go` - a bubbletea screen stack behind bare
  `descendence`. Menu → runs (live-refreshing table) → run detail (with
  cancel) → live log viewer, plus a new-run form, identity and config
  screens. Recorded as decision #22.
  Every flag command is untouched. Bare `descendence` *without* a terminal
  still prints usage and exits 2.
Broken / unresolved:
  - **`isTTY` called /dev/null a terminal.** It tested for
    `os.ModeCharDevice`, which /dev/null has, so `descendence > /dev/null`
    would have tried to open a full-screen app on it. Harmless while the
    check only chose between two ways of printing (its job since 1.19); not
    harmless once it decides whether to launch an application. Now asks the
    terminal driver via `charmbracelet/x/term.IsTerminal`.
  - **The root model caught `errMsg` and quit.** Every screen has its own
    handling that shows the error and carries on, and none of it could ever
    run, because the root intercepted the message first - so a single failed
    list refresh would have dropped the user back to their shell. The root
    now forwards it like any other message. There is a test.
Next action: unchanged - Phase 3, task 3.1.
Notes to future me:
  - **HISTORY's warning about TUI test harnesses was right, twice.** A pty
    capture produced nothing readable (lipgloss queries the terminal
    background and a harness has no answer), and a harness that executed
    tea.Cmds recursively hung forever on `textinput.Blink`, which blocks on
    a channel the real runtime feeds. Both cost real time. Test `Update`
    and `View` directly; they are pure functions of (model, msg).
  - Screens go back by emitting `popScreen` themselves rather than the root
    intercepting `esc`. A screen with a focused text field needs `esc` to
    mean something else, and only the screen knows that. `ctrl+c` is the
    exception and is caught at the root unconditionally - an application you
    cannot reliably leave is worse than no application.
  - The runs list *merges* refreshes instead of replacing. Replacing every
    two seconds would discard the pages the user had scrolled into and reset
    their cursor. Merging is only correct because runs are ordered
    queued_at DESC, so new ones always belong at the head.
  - The log viewer stops following the moment you scroll up and resumes when
    you return to the bottom, inferred from `viewport.AtBottom()` rather
    than from which key was pressed - so it works for arrows, page keys and
    the mouse wheel without enumerating them.
  - Leaving the log viewer cancels its stream context. Without that, the
    follow goroutine and its HTTP connection would outlive the screen for
    the rest of the run: the client-side version of the leak 2.7 fixed on
    the server.
