package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// These exercise the bubbletea model directly rather than through a
// terminal. Driving a real TUI needs a pty that answers capability queries,
// which makes for a slow and flaky test; Update/View are pure functions of
// (model, msg), so calling them is both faster and a stricter check.

func testModel(run client.Run) watchModel {
	return newWatchModel(context.Background(), client.New("http://example.invalid", "t"), run)
}

func queuedRun() client.Run {
	return client.Run{ID: 7, State: client.StateQueued, ImageRef: "alpine", Argv: []string{"echo", "hi"}, TimeoutSeconds: 60}
}

// quits reports whether cmd is (or resolves to) tea.Quit. Commands are
// funcs, so they can't be compared - run it and inspect the message.
func quits(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestWatchQuitsOnTerminalState(t *testing.T) {
	m := testModel(queuedRun())

	done := queuedRun()
	done.State = client.StateSucceeded

	updated, cmd := m.Update(runUpdateMsg{done})
	if !quits(t, cmd) {
		t.Fatal("a terminal run did not stop the watch")
	}
	if got := updated.(watchModel).run.State; got != client.StateSucceeded {
		t.Errorf("model state = %q, want %q", got, client.StateSucceeded)
	}
}

func TestWatchKeepsPollingWhileNonTerminal(t *testing.T) {
	m := testModel(queuedRun())

	running := queuedRun()
	running.State = client.StateRunning

	_, cmd := m.Update(runUpdateMsg{running})
	if cmd == nil {
		t.Fatal("a non-terminal run scheduled no further poll; the watch would hang forever")
	}
}

// Ctrl-C (and q) stop the *watch*. Until Phase 2's cancel endpoint exists
// the run itself keeps going, and the final view has to say so rather than
// implying the container was killed.
func TestWatchInterruptSaysTheRunContinues(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	} {
		m := testModel(queuedRun())

		updated, cmd := m.Update(key)
		if !quits(t, cmd) {
			t.Fatalf("%s did not stop the watch", key)
		}

		final := updated.(watchModel)
		if !final.interrupted {
			t.Fatalf("%s did not mark the watch interrupted", key)
		}
		if view := final.View(); !strings.Contains(view, "still going") {
			t.Errorf("%s produced a view that doesn't say the run continues:\n%s", key, view)
		}
	}
}

func TestWatchQuitsOnPollError(t *testing.T) {
	m := testModel(queuedRun())

	updated, cmd := m.Update(errMsg{errors.New("connection refused")})
	if !quits(t, cmd) {
		t.Fatal("a poll error did not stop the watch")
	}
	if view := updated.(watchModel).View(); !strings.Contains(view, "connection refused") {
		t.Errorf("view does not surface the error:\n%s", view)
	}
}

func TestRunSummaryShowsOutcome(t *testing.T) {
	code := int32(42)
	reason := "exit code 42"
	started := time.Now().Add(-3 * time.Second)
	finished := started.Add(3 * time.Second)

	run := client.Run{
		ID:         9,
		State:      client.StateFailed,
		ImageRef:   "docker.io/library/alpine:latest",
		Argv:       []string{"sh", "-c", "exit 42"},
		ExitCode:   &code,
		StartedAt:  &started,
		FinishedAt: &finished,
	}
	run.FailureReason = &reason

	view := renderRunSummary(run, false)
	for _, want := range []string{"run 9", "failed", "alpine", "sh -c exit 42", "42", "3.0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "still going") {
		t.Error("a finished run should not claim the watch was interrupted")
	}
}

// A run's exit code becomes the CLI's, so `descendence run ...` composes in
// a shell the same way running the command locally would. The interesting
// case is exit code 0 on a *failed* run: it can't happen today, but a
// nil-vs-zero mix-up here would silently report failures as success.
func TestExitCodeFor(t *testing.T) {
	zero, fortyTwo := int32(0), int32(42)

	cases := []struct {
		name string
		run  client.Run
		want int
	}{
		{"succeeded", client.Run{State: client.StateSucceeded, ExitCode: &zero}, 0},
		{"non-zero exit", client.Run{State: client.StateFailed, ExitCode: &fortyTwo}, 42},
		{"timed out, no exit code", client.Run{State: client.StateFailed}, 1},
		{"lost", client.Run{State: client.StateLost}, 1},
		{"cancelled", client.Run{State: client.StateCancelled}, 1},
		{"succeeded without an exit code", client.Run{State: client.StateSucceeded}, 0},
	}

	for _, tc := range cases {
		if got := exitCodeFor(tc.run); got != tc.want {
			t.Errorf("%s: exit code = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		420 * time.Millisecond:  "0.4s",
		4200 * time.Millisecond: "4.2s",
		134 * time.Second:       "2m14s",
	}

	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
