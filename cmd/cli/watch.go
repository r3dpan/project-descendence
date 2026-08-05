package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// How often the CLI asks the API for a run's current state while watching
// it. Fast enough that a two-second run doesn't feel laggy, slow enough
// that a one-hour run isn't 3600 needless requests.
const pollInterval = 500 * time.Millisecond

// --- Messages ---

// runUpdateMsg carries one observed state of the run.
type runUpdateMsg struct{ run client.Run }

// errMsg means polling failed and the watch cannot continue.
type errMsg struct{ err error }

// --- Model ---

// watchModel renders a single run until it reaches a terminal state. It
// deliberately owns its own polling (a tea.Cmd per tick) rather than
// reusing client.PollRun: bubbletea's loop must never block, and PollRun
// blocks by design for the non-TTY path.
type watchModel struct {
	ctx     context.Context
	client  *client.Client
	spinner spinner.Model

	run     client.Run
	started time.Time

	// interrupted records that the *watch* was stopped by the user. The run
	// itself keeps going - there is no cancel endpoint until Phase 2 - and
	// the final view says so rather than implying the run was killed.
	interrupted bool
	err         error
}

func newWatchModel(ctx context.Context, c *client.Client, run client.Run) watchModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = stateStyle(client.StateRunning)

	return watchModel{
		ctx:     ctx,
		client:  c,
		spinner: s,
		run:     run,
		started: time.Now(),
	}
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.pollAfter(pollInterval))
}

// pollAfter returns a command that waits, fetches the run once, and reports
// the result. One poll per command - bubbletea schedules the next one when
// this one's message arrives, so polls can never pile up.
func (m watchModel) pollAfter(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		run, err := m.client.GetRun(m.ctx, m.run.ID)
		if err != nil {
			return errMsg{err}
		}
		return runUpdateMsg{run}
	})
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.interrupted = true
			return m, tea.Quit
		}

	case runUpdateMsg:
		m.run = msg.run
		if m.run.IsTerminal() {
			return m, tea.Quit
		}
		return m, m.pollAfter(pollInterval)

	case errMsg:
		m.err = msg.err
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m watchModel) View() string {
	if m.err != nil {
		return styleError.Render("error: ") + m.err.Error() + "\n"
	}

	// Terminal (or interrupted): render the final summary and leave it in
	// the scrollback. This is what the user sees after the program exits.
	if m.run.IsTerminal() || m.interrupted {
		return renderRunSummary(m.run, m.interrupted)
	}

	var b strings.Builder
	b.WriteString(m.spinner.View())
	b.WriteString(" ")
	b.WriteString(styleBold.Render(fmt.Sprintf("run %d", m.run.ID)))
	b.WriteString("  ")
	b.WriteString(stateStyle(m.run.State).Render(m.run.State))
	b.WriteString("\n")
	b.WriteString(styleHint.Render(fmt.Sprintf("   %s elapsed · timeout %ds · q to stop watching",
		formatDuration(time.Since(m.started)), m.run.TimeoutSeconds)))
	b.WriteString("\n")

	return b.String()
}

// renderRunSummary is the block printed once a run is finished (or the
// watch was interrupted). Shared with the non-TTY path and with
// `runs get`, so a run always looks the same however you arrived at it.
func renderRunSummary(run client.Run, interrupted bool) string {
	var b strings.Builder

	b.WriteString(styleBold.Render(fmt.Sprintf("run %d", run.ID)))
	b.WriteString("  ")
	b.WriteString(stateStyle(run.State).Render(run.State))
	b.WriteString("\n")

	field := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString("  ")
		// Wide enough for the longest label ("container") plus a space.
		b.WriteString(styleLabel.Render(fmt.Sprintf("%-10s", label)))
		b.WriteString(styleValue.Render(value))
		b.WriteString("\n")
	}

	field("image", run.ImageRef)
	field("argv", strings.Join(run.Argv, " "))
	if run.ContainerID != nil && *run.ContainerID != "" {
		// Short form, as podman itself prints it - enough to paste into
		// `podman logs` or `podman inspect`, which is the whole reason for
		// showing it.
		id := *run.ContainerID
		field("container", id[:min(12, len(id))])
	}
	if run.ExitCode != nil {
		field("exit", fmt.Sprintf("%d", *run.ExitCode))
	}
	if run.FailureReason != nil && *run.FailureReason != "" {
		field("reason", *run.FailureReason)
	}
	if run.StartedAt != nil && run.FinishedAt != nil {
		field("duration", formatDuration(run.FinishedAt.Sub(*run.StartedAt)))
	}

	if interrupted {
		b.WriteString(styleHint.Render("  stopped watching; the run itself is still going"))
		b.WriteString("\n")
	}

	return b.String()
}

// formatDuration keeps elapsed times short and readable: sub-minute values
// get one decimal ("4.2s"), longer ones lose the noise ("2m14s").
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}
