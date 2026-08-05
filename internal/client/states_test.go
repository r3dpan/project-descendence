package client

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/r3dpan/project-descendence/internal/store"
)

// The six run states are written down in three places that must agree:
// internal/store (authoritative for Go), the runs_state_check constraint in
// the migration, and the Run schema's enum in api/openapi.yaml. Drift
// between them is the sort of thing that shows up as a run silently never
// being listed, so it is checked rather than trusted.
//
// The database's copy is verified separately, against a live Postgres.

func allStates() []string {
	return []string{
		store.StateQueued,
		store.StateRunning,
		store.StateSucceeded,
		store.StateFailed,
		store.StateCancelled,
		store.StateLost,
	}
}

// This package deliberately declares its own constants rather than
// importing the server's - it models a wire contract, not a database. That
// makes drift possible, so it is asserted.
func TestClientStatesMatchTheServer(t *testing.T) {
	pairs := map[string]string{
		StateQueued:    store.StateQueued,
		StateRunning:   store.StateRunning,
		StateSucceeded: store.StateSucceeded,
		StateFailed:    store.StateFailed,
		StateCancelled: store.StateCancelled,
		StateLost:      store.StateLost,
	}

	if len(pairs) != 6 {
		t.Fatalf("client and server states do not line up one-to-one: %d distinct values", len(pairs))
	}
	for clientState, serverState := range pairs {
		if clientState != serverState {
			t.Errorf("client state %q does not match the server's %q", clientState, serverState)
		}
	}
}

func TestRunIsTerminalMatchesTheServer(t *testing.T) {
	for _, state := range allStates() {
		run := Run{State: state}
		if got, want := run.IsTerminal(), store.IsTerminal(state); got != want {
			t.Errorf("Run{State: %q}.IsTerminal() = %v, but the server says %v", state, got, want)
		}
	}
}

// The spec is the contract (ARCHITECTURE.md decision #11), so an enum that
// has fallen behind the code is a real defect, not a documentation nit.
func TestOpenAPIEnumMatchesTheStates(t *testing.T) {
	const specPath = "../../api/openapi.yaml"

	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading %s: %v", specPath, err)
	}

	// The Run schema's state enum is the one written inline as a single
	// bracketed list, e.g. "enum: [queued, running, ...]".
	match := regexp.MustCompile(`enum: \[(queued[^\]]*)\]`).FindSubmatch(spec)
	if match == nil {
		t.Fatalf("could not find the run state enum in %s", specPath)
	}

	var found []string
	for _, raw := range strings.Split(string(match[1]), ",") {
		found = append(found, strings.TrimSpace(raw))
	}

	want := allStates()
	slices.Sort(found)
	slices.Sort(want)

	if !slices.Equal(found, want) {
		t.Errorf("%s lists states %v, but the code has %v", specPath, found, want)
	}
}
