package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The new-run form.
//
// The one place the TUI has to be careful about a rule the rest of the system
// takes seriously: **argv is an array, never a shell string** (task 1.11).
// A single text field is a natural way to type a command and a natural way to
// smuggle one, so what the field means has to be unambiguous. It is split on
// whitespace here and sent as an array; nothing is ever handed to a shell, on
// this side or the server's. The help text says so, because a user typing
// `sh -c 'a; b'` should understand that the quoting is being handled by
// `sh` inside the container and not by anything here.

const (
	fieldImage = iota
	fieldArgv
	fieldTimeout
	fieldCount
)

type runCreatedMsg struct{ run client.Run }

type formScreen struct {
	ctx    context.Context
	client *client.Client

	inputs  []textinput.Model
	focused int
	err     string
	busy    bool
}

func newFormScreen(ctx context.Context, c *client.Client) formScreen {
	m := formScreen{ctx: ctx, client: c, inputs: make([]textinput.Model, fieldCount)}

	image := textinput.New()
	image.Placeholder = "docker.io/library/alpine:latest"
	image.Prompt = ""
	image.CharLimit = 512
	image.Focus()

	argv := textinput.New()
	argv.Placeholder = `sh -c "echo hello"`
	argv.Prompt = ""
	argv.CharLimit = 4096

	timeout := textinput.New()
	timeout.Placeholder = "server default (3600)"
	timeout.Prompt = ""
	timeout.CharLimit = 8

	m.inputs[fieldImage] = image
	m.inputs[fieldArgv] = argv
	m.inputs[fieldTimeout] = timeout

	return m
}

func (m formScreen) Init() tea.Cmd { return textinput.Blink }

func (m formScreen) Title() string { return "new run" }

func (m formScreen) Help() string {
	if m.busy {
		return "creating…"
	}
	return "tab/↑↓ move · enter submit"
}

func (m formScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, popScreen()
		case "tab", "down":
			m.focus(m.focused + 1)
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus(m.focused - 1)
			return m, textinput.Blink
		case "enter":
			if m.busy {
				return m, nil
			}
			params, err := m.params()
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.err = ""
			m.busy = true
			return m, m.create(params)
		}

	case runCreatedMsg:
		m.busy = false
		// Straight into the output: the reason to start a run interactively
		// is almost always to watch what it does, and making that the default
		// saves the step that would otherwise follow every single time.
		return m, tea.Batch(
			pushScreen(newLogsScreen(m.ctx, m.client, msg.run.ID)),
			setStatus("created run %d", msg.run.ID),
		)

	case errMsg:
		m.busy = false
		m.err = msg.err.Error()
		return m, nil
	}

	var cmd tea.Cmd
	m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)

	return m, cmd
}

func (m *formScreen) focus(index int) {
	// Wrap, so tab cycles rather than sticking at the last field.
	m.focused = (index + fieldCount) % fieldCount

	for i := range m.inputs {
		if i == m.focused {
			m.inputs[i].Focus()
			continue
		}
		m.inputs[i].Blur()
	}
}

// params validates the form against the same rules the API enforces, so a
// mistake is caught while the cursor is still in the field rather than coming
// back as a 400 with the form already dismissed.
func (m formScreen) params() (client.CreateRunParams, error) {
	image := strings.TrimSpace(m.inputs[fieldImage].Value())
	if image == "" {
		return client.CreateRunParams{}, errors.New("an image reference is required")
	}

	// strings.Fields, deliberately: it splits on whitespace and nothing else,
	// so no quoting, escaping or globbing is interpreted here. What the user
	// typed as separate words is what the container receives as separate argv
	// elements, and a shell metacharacter is just a character.
	argv := strings.Fields(m.inputs[fieldArgv].Value())
	if len(argv) == 0 {
		return client.CreateRunParams{}, errors.New("a command is required")
	}

	params := client.CreateRunParams{ImageRef: image, Argv: argv}

	if raw := strings.TrimSpace(m.inputs[fieldTimeout].Value()); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || seconds <= 0 {
			return client.CreateRunParams{}, errors.New("timeout must be a positive number of seconds")
		}
		params.TimeoutSeconds = int32(seconds)
	}

	return params, nil
}

func (m formScreen) create(params client.CreateRunParams) tea.Cmd {
	return func() tea.Msg {
		run, err := m.client.CreateRun(m.ctx, params)
		if err != nil {
			return errMsg{err}
		}
		return runCreatedMsg{run}
	}
}

func (m formScreen) View() string {
	labels := [fieldCount]string{"image", "command", "timeout"}

	var b strings.Builder
	for i, input := range m.inputs {
		marker := "  "
		if i == m.focused {
			marker = stateStyle(client.StateRunning).Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", marker,
			styleLabel.Render(fmt.Sprintf("%-9s", labels[i])), input.View()))
	}

	b.WriteString("\n")
	b.WriteString(styleHint.Render("  the command is split on spaces and sent as an array — never a shell string,"))
	b.WriteString("\n")
	b.WriteString(styleHint.Render("  so use `sh -c \"...\"` if you want shell syntax inside the container"))
	b.WriteString("\n")

	if m.err != "" {
		b.WriteString("\n  " + styleError.Render(m.err) + "\n")
	}

	return b.String()
}

// --- Identity and configuration ---

type whoAmIScreen struct {
	ctx       context.Context
	client    *client.Client
	principal *client.Principal
	err       string
}

type principalMsg struct{ principal client.Principal }

func newWhoAmIScreen(ctx context.Context, c *client.Client) whoAmIScreen {
	return whoAmIScreen{ctx: ctx, client: c}
}

func (m whoAmIScreen) Init() tea.Cmd {
	return func() tea.Msg {
		principal, err := m.client.WhoAmI(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return principalMsg{principal}
	}
}

func (m whoAmIScreen) Title() string { return "identity" }
func (m whoAmIScreen) Help() string  { return "" }

func (m whoAmIScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "left", "h", "enter":
			return m, popScreen()
		}
	case principalMsg:
		m.principal = &msg.principal
	case errMsg:
		m.err = msg.err.Error()
	}

	return m, nil
}

func (m whoAmIScreen) View() string {
	if m.err != "" {
		return "  " + styleError.Render(m.err) + "\n"
	}
	if m.principal == nil {
		return styleHint.Render("  resolving…") + "\n"
	}

	var b strings.Builder
	field := func(label, value string) {
		b.WriteString("  " + styleLabel.Render(fmt.Sprintf("%-9s", label)) + styleValue.Render(value) + "\n")
	}

	b.WriteString("  " + styleBold.Render(m.principal.Name) + "\n")
	field("id", fmt.Sprintf("#%d", m.principal.ID))
	field("kind", m.principal.Kind)
	field("role", m.principal.Role)
	field("permissions", strings.Join(m.principal.Permissions, ", "))

	return b.String()
}

type configScreen struct{ cfg config }

func newConfigScreen(cfg config) configScreen { return configScreen{cfg: cfg} }

func (m configScreen) Init() tea.Cmd { return nil }
func (m configScreen) Title() string { return "configuration" }
func (m configScreen) Help() string  { return "" }

func (m configScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q", "left", "h", "enter":
			return m, popScreen()
		}
	}
	return m, nil
}

func (m configScreen) View() string {
	var b strings.Builder

	field := func(label, value string, from source) {
		b.WriteString("  " + styleLabel.Render(fmt.Sprintf("%-8s", label)) +
			styleValue.Render(value) + " " + styleHint.Render("("+string(from)+")") + "\n")
	}

	field("url", m.cfg.baseURL, m.cfg.urlSource)
	// The token is shown only as its trailing characters, the same hint the
	// server stores - enough to tell two tokens apart, useless to anyone
	// reading over a shoulder or watching a screen share.
	field("token", tokenHint(m.cfg.token), m.cfg.tokenSource)

	b.WriteString("  " + styleLabel.Render(fmt.Sprintf("%-8s", "file")) + styleValue.Render(m.cfg.path) + "\n")
	if _, err := os.Stat(m.cfg.path); err != nil {
		b.WriteString(styleHint.Render("           (does not exist)") + "\n")
	}

	return b.String()
}

// parseRunIDInput validates a hand-typed run id.
func parseRunIDInput(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("enter a run id")
	}

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a run id", raw)
	}

	return id, nil
}
