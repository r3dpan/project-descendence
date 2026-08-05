package store

import "testing"

func TestIsTerminal(t *testing.T) {
	terminal := map[string]bool{
		StateQueued:    false,
		StateRunning:   false,
		StateSucceeded: true,
		StateFailed:    true,
		StateCancelled: true,
		StateLost:      true,
	}

	if len(terminal) != 6 {
		t.Fatalf("expected exactly six states, have %d - update this test and the docs alongside", len(terminal))
	}

	for state, want := range terminal {
		if got := IsTerminal(state); got != want {
			t.Errorf("IsTerminal(%q) = %v, want %v", state, got, want)
		}
	}

	// An unrecognised state must not be treated as terminal: the reconciler
	// and ListNonTerminalRuns would then quietly ignore a run forever.
	if IsTerminal("not-a-state") {
		t.Error("an unknown state was reported terminal")
	}
}
