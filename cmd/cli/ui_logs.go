package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The live log viewer.
//
// The interesting constraint is that bubbletea's Update must never block, and
// client.FollowRunLogs blocks for the length of the run by design. So the
// follow runs in its own goroutine writing to a channel, and the screen reads
// that channel one message at a time through a tea.Cmd: each delivered line
// schedules the next read. Nothing is ever waited on inside Update.
//
// The channel is buffered because a script can print far faster than a
// terminal can be redrawn, and an unbuffered one would make the follow
// goroutine wait for the UI - turning a slow render into backpressure all the
// way down to the log reader.

const logChannelDepth = 256

// logStreamStyle colours stderr differently from stdout. This is the one
// place the CLI adds anything to a script's own output, and only in the
// interactive viewer - `descendence logs` still prints raw text, because that
// output gets piped into things that would choke on escape codes.
var logStderrStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})

type logLineMsg client.LogLine
type logStateMsg struct{ state string }
type logEndedMsg struct{ err error }

type logsScreen struct {
	ctx    context.Context
	runID  int64
	cancel context.CancelFunc

	events   chan tea.Msg
	viewport viewport.Model
	ready    bool

	lines []client.LogLine
	state string
	// following is whether new output should scroll the viewport. Turned off
	// the moment the user scrolls up, because yanking someone back to the
	// bottom while they are reading is the single most annoying thing a log
	// viewer can do, and back on when they return to the bottom themselves.
	following bool
	done      bool
	err       error
}

func newLogsScreen(ctx context.Context, c *client.Client, runID int64) *logsScreen {
	streamCtx, cancel := context.WithCancel(ctx)

	m := &logsScreen{
		ctx:       streamCtx,
		runID:     runID,
		cancel:    cancel,
		events:    make(chan tea.Msg, logChannelDepth),
		following: true,
	}

	// Pointer receiver, and therefore a pointer screen: this one owns a
	// goroutine and a cancel function, and copying it around by value would
	// mean the copy that gets popped is not the copy that owns them.
	go m.follow(c)

	return m
}

// follow streams the run in the background, turning everything into messages
// on the events channel. It is the only writer, and it closes the channel on
// the way out so the reader can tell "no more" from "nothing yet".
func (m *logsScreen) follow(c *client.Client) {
	defer close(m.events)

	err := c.FollowRunLogs(m.ctx, m.runID, 0, client.LogStream{
		OnLine: func(line client.LogLine) {
			m.events <- logLineMsg(line)
		},
		OnState: func(state string) {
			m.events <- logStateMsg{state}
		},
	})

	// A cancelled context is this screen being closed, not a failure.
	if err != nil && m.ctx.Err() == nil {
		m.events <- logEndedMsg{err}
		return
	}

	m.events <- logEndedMsg{}
}

// waitForEvent reads exactly one message from the stream. Returning a message
// causes bubbletea to call Update, which schedules the next read - so this is
// a loop without a loop, and without ever blocking the UI.
func (m *logsScreen) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.events
		if !ok {
			return logEndedMsg{}
		}
		return msg
	}
}

func (m *logsScreen) Init() tea.Cmd { return m.waitForEvent() }

func (m *logsScreen) Title() string { return fmt.Sprintf("run %d output", m.runID) }

func (m *logsScreen) Help() string {
	if m.done {
		return "↑/↓ scroll · g/G top/bottom"
	}
	if m.following {
		return "↑/↓ scroll · following (scroll up to pause)"
	}
	return "↑/↓ scroll · G resume following"
}

func (m *logsScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		height := max(msg.Height-chromeHeight, 3)
		if !m.ready {
			m.viewport = viewport.New(msg.Width, height)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, height
		}
		m.render()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "left", "h":
			// Stop the stream on the way out. Without this, leaving the
			// screen would leave the follow goroutine and its HTTP
			// connection running for the rest of the run - the client-side
			// version of exactly the leak task 2.7 fixed on the server.
			m.cancel()
			return m, popScreen()
		case "G", "end":
			m.following = true
			m.viewport.GotoBottom()
			return m, nil
		case "g", "home":
			m.following = false
			m.viewport.GotoTop()
			return m, nil
		}

	case logLineMsg:
		m.lines = append(m.lines, client.LogLine(msg))
		m.render()
		return m, m.waitForEvent()

	case logStateMsg:
		m.state = msg.state
		return m, m.waitForEvent()

	case logEndedMsg:
		m.done = true
		m.err = msg.err
		if msg.err != nil {
			return m, setStatus("stream ended: %v", msg.err)
		}
		return m, nil
	}

	if !m.ready {
		return m, nil
	}

	before := m.viewport.AtBottom()
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)

	// Scrolling away from the bottom pauses following; coming back resumes
	// it. Inferred from where the viewport ended up rather than from which
	// key was pressed, so it works for every way of moving (arrows, page
	// keys, mouse wheel) without enumerating them.
	if at := m.viewport.AtBottom(); at != before || !at {
		m.following = at
	}

	return m, cmd
}

// render rebuilds the viewport's content from the lines received so far.
//
// Rebuilt wholesale rather than appended to because viewport holds a single
// string; for the volumes a person actually watches this is not worth
// optimising, and a run that prints 20000 lines is one you read with
// `descendence logs` and a pager, not by scrolling a TUI.
func (m *logsScreen) render() {
	if !m.ready {
		return
	}

	var b strings.Builder
	for i, line := range m.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if line.Stream == "stderr" {
			b.WriteString(logStderrStyle.Render(line.Text))
			continue
		}
		b.WriteString(line.Text)
	}

	m.viewport.SetContent(b.String())
	if m.following {
		m.viewport.GotoBottom()
	}
}

func (m *logsScreen) View() string {
	if !m.ready {
		return styleHint.Render("  connecting…") + "\n"
	}

	var b strings.Builder
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	status := fmt.Sprintf("%d lines", len(m.lines))
	if m.state != "" {
		status += " · " + stateStyle(m.state).Render(m.state)
	}
	if len(m.lines) == 0 && !m.done {
		status += styleHint.Render(" · waiting for output")
	}
	b.WriteString("  " + styleLabel.Render(status))

	return b.String()
}

// --- Picking a run by id ---

// runPickerScreen is the "Logs by run id" entry: a single field, because
// typing an id you already know should not require scrolling a table to find
// it.
type runPickerScreen struct {
	ctx    context.Context
	client *client.Client
	input  string
	err    string
}

func newRunPickerScreen(ctx context.Context, c *client.Client) runPickerScreen {
	return runPickerScreen{ctx: ctx, client: c}
}

func (m runPickerScreen) Init() tea.Cmd { return nil }
func (m runPickerScreen) Title() string { return "logs by id" }
func (m runPickerScreen) Help() string  { return "type a run id · enter follow" }

func (m runPickerScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "esc":
		return m, popScreen()
	case "enter":
		id, err := parseRunIDInput(m.input)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.err = ""
		return m, pushScreen(newLogsScreen(m.ctx, m.client, id))
	case "backspace":
		if m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
	default:
		// Digits only: the field accepts exactly what a run id can be, so a
		// typo is refused as it is typed rather than after enter.
		for _, r := range key.String() {
			if r >= '0' && r <= '9' {
				m.input += string(r)
			}
		}
	}

	return m, nil
}

func (m runPickerScreen) View() string {
	var b strings.Builder
	b.WriteString("  " + styleLabel.Render("run id  "))
	b.WriteString(styleBold.Render(m.input + "▏"))
	b.WriteString("\n")
	if m.err != "" {
		b.WriteString("  " + styleError.Render(m.err) + "\n")
	}
	return b.String()
}
