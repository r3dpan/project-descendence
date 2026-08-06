package main

import (
	"context"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The users list - a browse view for the "Users" menu entry (Phase 8, task
// 8.9), gated at the menu level to admins only (see ui_menu.go). Unlike
// runsScreen this is not a growing timeline, so there is no refresh tick or
// pagination - the same reasoning ListUsersHandler already applies
// server-side (a homelab has a handful of users, not thousands). Creating,
// reassigning a role and revoking stay flag-command-only (`descendence user
// ...`) rather than duplicated here as an in-TUI form - this screen answers
// "who has access", the flag commands are what changes it.
type usersScreen struct {
	ctx    context.Context
	client *client.Client

	table   table.Model
	users   []client.User
	loading bool
	err     error
}

type usersLoadedMsg struct{ list client.UserList }

func newUsersScreen(ctx context.Context, c *client.Client) usersScreen {
	m := usersScreen{ctx: ctx, client: c}

	m.table = table.New(
		table.WithColumns(userColumns(defaultWidth)),
		table.WithFocused(true),
	)
	m.table.SetStyles(tableStyles())
	m.table.SetHeight(tableHeight(0))

	return m
}

func (m usersScreen) Init() tea.Cmd {
	m.loading = true
	return m.fetch()
}

func (m usersScreen) Title() string { return "users" }
func (m usersScreen) Help() string  { return "↑/↓ move · r refresh" }

func (m usersScreen) fetch() tea.Cmd {
	return func() tea.Msg {
		list, err := m.client.ListUsers(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return usersLoadedMsg{list}
	}
}

func (m usersScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.table.SetColumns(userColumns(msg.Width))
		m.table.SetHeight(min(tableHeight(len(m.users)), max(msg.Height-chromeHeight, 3)))
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "left", "h":
			return m, popScreen()
		case "r":
			m.loading = true
			return m, m.fetch()
		}

	case usersLoadedMsg:
		m.loading = false
		m.err = nil
		m.users = msg.list.Items
		m.table.SetRows(userRows(m.users))
		m.table.SetHeight(tableHeight(len(m.users)))
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, setStatus("refresh failed: %v", msg.err)
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m usersScreen) View() string {
	if m.err != nil && len(m.users) == 0 {
		return "  " + styleError.Render(m.err.Error()) + "\n"
	}
	if m.loading && len(m.users) == 0 {
		return styleHint.Render("  loading…") + "\n"
	}
	if len(m.users) == 0 {
		return styleHint.Render("  No users. Create one with: descendence user create -name <name> -role <role>") + "\n"
	}
	return m.table.View() + "\n"
}

func userColumns(width int) []table.Column {
	const (
		idW   = 6
		roleW = 10
		// bubbles/table pads every cell by one column on each side.
		padding     = 2 * 4
		minFlexible = 16
	)
	remaining := max(width-(idW+roleW+padding), 2*minFlexible)
	nameW := remaining / 2
	createdW := remaining - nameW

	return []table.Column{
		{Title: "ID", Width: idW},
		{Title: "NAME", Width: nameW},
		{Title: "ROLE", Width: roleW},
		{Title: "CREATED", Width: createdW},
	}
}

func userRows(users []client.User) []table.Row {
	rows := make([]table.Row, len(users))
	for i, u := range users {
		role := u.Role
		if u.RevokedAt != nil {
			role += " (revoked)"
		}
		rows[i] = table.Row{
			strconv.FormatInt(u.ID, 10),
			u.Name,
			role,
			u.CreatedAt,
		}
	}
	return rows
}
