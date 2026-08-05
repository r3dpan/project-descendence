// This file is hand-written and lives alongside the sqlc-generated code in
// this package. sqlc only rewrites its own outputs (db.go, models.go,
// *.sql.go), so it survives `sqlc generate`.

package store

// The six run states (PLAN.md task 1.14). This is the authoritative list
// for Go code; the database enforces the same set in runs_state_check, and
// api/openapi.yaml repeats it as the Run schema's enum. All three must
// agree - if you add a state, change all three.
//
// The state machine, and who performs each transition:
//
//	                      ┌──────────────────────► cancelled   (Phase 2, task 2.8)
//	                      │
//	(api: CreateRun)      │      (supervisor: FinishRun)
//	        │             │        ┌──────────► succeeded
//	        ▼             │        │
//	     queued ──────────┴────────┼──────────► failed
//	        │  (supervisor:        │
//	        │   ClaimNextQueuedRun)│
//	        ▼                      │
//	     running ──────────────────┴──────────► lost
//	                                  (supervisor: reconcile, on restart)
//
// queued and running are the only non-terminal states. A terminal state is
// final: nothing ever transitions out of one, and FinishRun refuses to
// overwrite one.
//
// cancelled currently has no producer - the cancel endpoint is Phase 2 task
// 2.8, which owns propagating cancellation and stopping the container. It
// is defined, constrained and rendered everywhere regardless, so that task
// only has to add the transition, not plumb a new state through.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateLost      = "lost"
)

// IsTerminal reports whether state is one a run never leaves.
func IsTerminal(state string) bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateLost:
		return true
	default:
		return false
	}
}
