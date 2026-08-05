package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The interactive application: one bubbletea program you open and navigate,
// as opposed to the per-command TUI views in watch.go and list.go which run
// for the length of one command and exit.
//
// It is an *addition*, not a replacement. Every flag command still works
// exactly as before, because they are what makes the CLI scriptable - exit
// codes propagate, output pipes, `-detach` composes - and ARCHITECTURE.md §2
// principle 3 has the API callable from other automation as a goal. Bare
// `descendence` opens this instead of printing usage, but only on a terminal:
// piped or redirected it keeps the old behaviour, so nothing scripted ever
// finds itself talking to a TUI (see cmdUIOrUsage).
//
// Structure is a screen stack. Each screen is self-contained and pushes or
// pops the next; the root model owns only the chrome, the terminal size and
// the transient status line. Screens go back by emitting popScreen rather
// than the root intercepting `esc`, because a screen with a text field in it
// needs `esc` to mean something else and only the screen knows that.

// uiScreen is one navigable view.
//
// It mirrors tea.Model but returns uiScreen rather than tea.Model, which
// removes a type assertion from every single Update and makes it impossible
// to accidentally return the wrong kind of model from a screen.
type uiScreen interface {
	Init() tea.Cmd
	Update(tea.Msg) (uiScreen, tea.Cmd)
	View() string
	// Title is the breadcrumb segment for this screen.
	Title() string
	// Help is the key hint line shown at the bottom.
	Help() string
}

// --- Navigation messages ---

// pushScreenMsg opens a screen on top of the current one.
type pushScreenMsg struct{ screen uiScreen }

// popScreenMsg returns to the screen underneath. Popping the last screen
// quits, so `esc` out of the root menu leaves the application.
type popScreenMsg struct{}

// statusMsg sets the transient line above the help hints - how a screen says
// "cancellation requested" or "run 42 created" without inventing a dialog.
type statusMsg struct{ text string }

func pushScreen(s uiScreen) tea.Cmd { return func() tea.Msg { return pushScreenMsg{s} } }
func popScreen() tea.Cmd            { return func() tea.Msg { return popScreenMsg{} } }
func setStatus(f string, a ...any) tea.Cmd {
	return func() tea.Msg { return statusMsg{fmt.Sprintf(f, a...)} }
}

// --- Root model ---

type uiModel struct {
	ctx    context.Context
	client *client.Client
	cfg    config

	stack  []uiScreen
	width  int
	height int

	status string
	err    error
}

func newUIModel(ctx context.Context, c *client.Client, cfg config) uiModel {
	m := uiModel{ctx: ctx, client: c, cfg: cfg, width: defaultWidth, height: 24}
	m.stack = []uiScreen{newMenuScreen(m.ctx, m.client, m.cfg)}
	return m
}

func (m uiModel) Init() tea.Cmd {
	return m.top().Init()
}

func (m uiModel) top() uiScreen { return m.stack[len(m.stack)-1] }

// chromeHeight is what the header, status and help lines cost, so screens
// that need to size themselves (the table, the log viewport) know how much
// of the terminal is actually theirs.
const chromeHeight = 6

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Forwarded on, so screens can resize too.

	case tea.KeyMsg:
		// Ctrl-C always quits, from anywhere, including out of a text field.
		// Nothing else is allowed to intercept it: an interactive app you
		// cannot reliably leave is worse than no interactive app.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case pushScreenMsg:
		m.stack = append(m.stack, msg.screen)
		m.status = ""
		// A pushed screen has never seen a WindowSizeMsg, so hand it the
		// current size immediately rather than leaving it at its default
		// until the user happens to resize the terminal.
		return m, tea.Batch(msg.screen.Init(), m.sizeCmd())

	case popScreenMsg:
		if len(m.stack) == 1 {
			return m, tea.Quit
		}
		m.stack = m.stack[:len(m.stack)-1]
		m.status = ""
		return m, m.sizeCmd()

	case statusMsg:
		m.status = msg.text
		return m, nil

		// errMsg is deliberately NOT handled here, and is forwarded to the
		// screen like anything else. Catching it at the root would mean any
		// failed request tore the whole application down - so one refresh
		// failing while the list sits idle would drop the user back to their
		// shell. Every screen that can produce one handles it, mostly by
		// showing it in the status line and carrying on.
	}

	updated, cmd := m.top().Update(msg)
	m.stack[len(m.stack)-1] = updated

	return m, cmd
}

// sizeCmd re-delivers the current terminal size. Used after a push or pop so
// the newly visible screen lays itself out for the real window.
func (m uiModel) sizeCmd() tea.Cmd {
	width, height := m.width, m.height
	return func() tea.Msg { return tea.WindowSizeMsg{Width: width, Height: height} }
}

func (m uiModel) View() string {
	if m.err != nil {
		return styleError.Render("error: ") + m.err.Error() + "\n"
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")
	b.WriteString(m.top().View())
	b.WriteString("\n")

	if m.status != "" {
		b.WriteString(styleValue.Render("  " + m.status))
		b.WriteString("\n")
	}

	back := "esc quit"
	if len(m.stack) > 1 {
		back = "esc back"
	}
	b.WriteString(styleHint.Render("  " + m.top().Help() + " · " + back + " · ctrl+c exit"))
	b.WriteString("\n")

	return b.String()
}

// renderHeader is the breadcrumb: where you are, and how you got there.
// Worth the two lines it costs - a stack-based UI with no trail is one where
// `esc` is a guess.
func (m uiModel) renderHeader() string {
	segments := make([]string, 0, len(m.stack))
	for _, screen := range m.stack {
		segments = append(segments, screen.Title())
	}

	trail := styleLabel.Render(strings.Join(segments[:len(segments)-1], " › "))
	current := styleBold.Render(segments[len(segments)-1])
	if trail != "" {
		current = trail + styleLabel.Render(" › ") + current
	}

	rule := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).
		Render(strings.Repeat("─", max(m.width, 10)))

	return "  " + current + "\n" + rule
}

// --- Entry point ---

// runUI opens the interactive application.
func runUI(ctx context.Context, c *client.Client, cfg config) int {
	// The alt screen is what makes this an application rather than command
	// output: it gets its own buffer and restores the terminal on exit,
	// leaving the user's scrollback exactly as they left it.
	model, err := tea.NewProgram(
		newUIModel(ctx, c, cfg),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	).Run()
	if err != nil {
		// A cancelled context is Ctrl-C or a signal, not a failure.
		if ctx.Err() != nil {
			return 130
		}
		printError(err)
		return 1
	}

	if final, ok := model.(uiModel); ok && final.err != nil {
		printError(final.err)
		return 1
	}

	return 0
}
