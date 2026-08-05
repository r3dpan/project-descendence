# CLAUDE.md

## What this is

Self-hosted script automation platform: scripts run in Podman containers, triggered
through an HTTP API, with state and history in Postgres. Go, Linux-first, single-user
homelab scale. Explicitly a learning project — public adoption is not a goal, and
that shapes several decisions (hand-written routing, hand-written API client, no
frameworks where the framework is the thing worth learning).

## Read these first

Three docs in `docs/`, and they are load-bearing — this project is worked on in bursts
with weeks in between:

- **`docs/ARCHITECTURE.md`** — the *what* and *why*. §6 is a numbered decision log;
  when a design choice looks arbitrary, the answer is there. Cite decisions by number
  in comments (`decision #19`) the way the existing code does.
- **`docs/PLAN.md`** — the *when*. Phased task list with `[ ] [~] [x] [!]` markers.
  The **"Current position"** block at the top is the fastest way to find out where
  things stand. Tasks are referenced by number throughout the code (`task 2.3`).
- **`docs/HISTORY.md`** — session log, newest at the bottom. What broke, what was
  learned, what was next.

**End-of-session ritual (both are expected, not optional):** update PLAN.md's
"Current position" block and its task markers, and append an entry to HISTORY.md.
When code contradicts a doc, fix the doc in the same change — §4.2 stayed wrong for
weeks because nobody did.

## Commands

Standard `go build` / `test` / `vet ./...` applies. The non-obvious ones:

```bash
sqlc generate                 # after editing internal/store/queries/*.sql
goose up                      # reads GOOSE_* from .env
go run ./cmd/seed             # once, against a fresh DB; prints the token once
go test -short ./...          # skips the deliberately-slow long-poll timeout tests
```

`api/openapi.yaml` is the API contract — update it in the same change as the handler.
Config is environment only; see `.env.sample`.

## Invariants worth not breaking

- **api and supervisor never talk to each other.** They communicate only through
  Postgres (SQL, `LISTEN`/`NOTIFY`, advisory lock) and the shared `RUN_LOG_DIR`.
  Both processes must be given the *same* log directory; the supervisor is its sole
  writer, the API only reads.
- **Log write ordering: flush the file → insert the index row → notify.** The index
  row is what tells a reader those bytes exist, so any other order publishes an
  offset pointing past the end of the file.
- **Notifications are watermarks and lossy by design.** They carry "run 42 has output
  through seq 900", never log text. Every consumer must *also* poll on a slow timer;
  one that trusts notifications alone hangs the first time the listener reconnects.
- **Run states live in three places that must agree:** `internal/store/states.go`,
  the `runs_state_check` constraint in the migration, and the `Run` schema enum in
  `api/openapi.yaml`. Terminal states are final — nothing transitions out of one.
- **Container argv is always an array, never a shell string.** This is the injection
  hole this class of tool is famous for; there is a test asserting it (task 1.11).
- **Runs pin image digests, not tags**, and record the commit SHA — a past run must
  be explainable and repeatable.
- **Every container is labelled with `run_id`.** Crash reconciliation depends on it.
- Only one supervisor may run at a time; a Postgres advisory lock enforces it.
- Runs are async: `POST` returns `202` with a `Location` header. Never block an HTTP
  request on a script finishing.

## Conventions

- **Hand-written, deliberately:** routing (stdlib `net/http` + Go 1.22 patterns), all
  handlers, the Podman client, the API client. No chi, no oapi-codegen, no cobra.
  Don't introduce them. The exception is the Charm stack for CLI rendering
  (decision #17) and `pgx`/`sqlc` for the database.
- `sqlc` only rewrites `db.go`, `models.go` and `*.sql.go`; hand-written files in
  `internal/store` survive `sqlc generate`. Query docs go in the `.sql` file.
- Errors are RFC 9457 `application/problem+json` via `writeProblem`.
- Lists use keyset (cursor) pagination, never offset.
- Comments explain *why*, and reference the decision or task that produced the rule.
  Match that density — this codebase's comments are unusually load-bearing.

## Testing

Integration tests skip themselves rather than fail when their dependency is absent
(`DATABASE_URL`, `PODMAN_SOCKET`, `DESCENDENCE_URL`), so a green `go test ./...` does
not mean much on its own — check what actually ran.

- Some tests sleep past a request timeout on purpose (they exist to catch the
  "blanket `http.Client.Timeout` truncates a long poll" bug, which has landed twice).
  They are behind `testing.Short()`.
- **Do not run `go test ./internal/store` against a database a live supervisor is
  polling** — it claims the tests' throwaway runs. Any test that claims work from a
  shared queue interferes with every other test, in both directions.
- Long-polling endpoints use `longPollClient`, not `httpClient`.

## Local gotchas

- `pkill -f 'go run ./cmd/supervisor'` matches the shell running it and kills your own
  session. Build a binary and `pkill -x supervisor`.
- WSL2: lingering keeps the user systemd manager alive across logout but does not stop
  WSL2 from shutting down on its own idle timer.
- No `psql` in this environment; DBeaver is the DB client in use.
