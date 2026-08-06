package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The root menu. Deliberately a hand-rolled list rather than bubbles/list:
// there are five fixed entries with no filtering, paging or multi-select, and
// bubbles/list is built for the opposite of that.

type menuEntry struct {
	label string
	hint  string
	open  func(m menuScreen) tea.Cmd
}

type menuScreen struct {
	ctx    context.Context
	client *client.Client
	cfg    config

	entries []menuEntry
	cursor  int
}

// newMenuScreen builds the root menu. role is the calling principal's
// resolved role (from a synchronous WhoAmI call in runUI, task 8.9) - the
// "Users" entry only appears for role == "admin", hidden rather than shown
// and then 403ing on selection: the TUI is a navigable app, and a dead-end
// action reads worse here than an absent one. Flag commands (`descendence
// user ...`) always exist regardless, since the server's 403 is the answer
// automation actually wants, not a client-side guess.
func newMenuScreen(ctx context.Context, c *client.Client, cfg config, role string) menuScreen {
	entries := []menuEntry{
		{
			label: "Runs",
			hint:  "browse, open, follow and cancel",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newRunsScreen(m.ctx, m.client))
			},
		},
		{
			label: "New run",
			hint:  "start a container from an image and argv",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newFormScreen(m.ctx, m.client))
			},
		},
		{
			label: "Logs by run id",
			hint:  "follow a run you already know the id of",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newRunPickerScreen(m.ctx, m.client))
			},
		},
	}

	if role == "admin" {
		entries = append(entries, menuEntry{
			label: "Users",
			hint:  "who has access (create/revoke: descendence user ...)",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newUsersScreen(m.ctx, m.client))
			},
		})
	}

	entries = append(entries,
		menuEntry{
			label: "Identity",
			hint:  "which principal this token resolves to",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newWhoAmIScreen(m.ctx, m.client))
			},
		},
		menuEntry{
			label: "Configuration",
			hint:  "where the URL and token are being read from",
			open: func(m menuScreen) tea.Cmd {
				return pushScreen(newConfigScreen(m.cfg))
			},
		},
	)

	return menuScreen{
		ctx:     ctx,
		client:  c,
		cfg:     cfg,
		entries: entries,
	}
}

func (m menuScreen) Init() tea.Cmd { return nil }
func (m menuScreen) Title() string { return "descendence" }
func (m menuScreen) Help() string  { return "↑/↓ move · enter open" }

func (m menuScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.entries) - 1
	case "enter", "l", "right":
		return m, m.entries[m.cursor].open(m)
	case "esc", "q":
		// Popping the last screen quits, which is what the root does with it.
		return m, popScreen()
	}

	return m, nil
}

func (m menuScreen) View() string {
	var b strings.Builder

	for i, entry := range m.entries {
		marker := "  "
		label := styleValue.Render(entry.label)
		if i == m.cursor {
			marker = stateStyle(client.StateRunning).Render("▸ ")
			label = styleBold.Render(entry.label)
		}

		b.WriteString(fmt.Sprintf("  %s%-18s%s\n", marker, label, styleHint.Render(entry.hint)))
	}

	return b.String()
}
