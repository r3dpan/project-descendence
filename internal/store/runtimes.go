// This file is hand-written and lives alongside the sqlc-generated code in
// this package. sqlc only rewrites its own outputs (db.go, models.go,
// *.sql.go), so it survives `sqlc generate`.

package store

// The four runtime build states (PLAN.md task 4.1). This is the
// authoritative list for Go code; the database enforces the same set in
// runtimes_build_status_check, and api/openapi.yaml repeats it as the
// Runtime schema's enum. All three must agree - if you add a state, change
// all three. Mirrors states.go's rule for runs.
//
//	created ──► pending ──(supervisor: ClaimNextPendingRuntimeBuild)──► building ──┬──► ready
//	   ▲            ▲                                                              │
//	   │            └──────────────(api: RequestRuntimeBuild, from ready/failed)───┤
//	   │                                                                           └──► failed
//	CreateRuntime
//
// pending and building are the only non-terminal states; ready and failed
// both accept a new build request (RequestRuntimeBuild), which is the one
// difference from runs' state machine - a runtime is a reusable definition,
// not a one-shot unit of work, so "done" is not final the way a run's
// terminal states are.
const (
	BuildStatusPending  = "pending"
	BuildStatusBuilding = "building"
	BuildStatusReady    = "ready"
	BuildStatusFailed   = "failed"
)

// The three runtime languages a job manifest's `runtime:` can name
// (ARCHITECTURE.md §4.4). Determines which install-step template
// internal/runtimebuild renders.
const (
	LangPython     = "python"
	LangPowerShell = "powershell"
	LangNode       = "node"
)
