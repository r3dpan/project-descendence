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
//	                    (api: CancelQueuedRun)
//	        ┌──────────────────────────────────► cancelled
//	        │                                        ▲
//	(api: CreateRun)                                 │ (supervisor: cancelWatch,
//	        │                    (supervisor:        │  on runs.cancel_requested_at)
//	        ▼                     FinishRun)         │
//	     queued                    ┌──────────► succeeded
//	        │                      │
//	        │  (supervisor:        ├──────────► failed
//	        │   ClaimNextQueuedRun)│
//	        ▼                      │
//	     running ──────────────────┴──────────► lost
//	                                  (supervisor: reconcile, on restart)
//
// queued and running are the only non-terminal states. A terminal state is
// final: nothing ever transitions out of one, and FinishRun refuses to
// overwrite one.
//
// cancelled has two producers, because cancelling means two different things
// depending on where the run is (task 2.8). A *queued* run has no container,
// so the API cancels it outright in one guarded UPDATE - the only terminal
// state the API ever writes. A *running* run belongs to the supervisor, which
// is the only process allowed to stop a container, so the API records the
// request in runs.cancel_requested_at and the supervisor performs it. The two
// processes never talk (ARCHITECTURE.md §3); that column is the whole channel
// between them in this direction.
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
