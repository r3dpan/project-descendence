package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// The runs list and one run's detail view.
//
// Distinct from list.go's listModel, which backs `descendence runs list` and
// is a one-shot: it quits on enter and prints the chosen run. This one stays
// open, refreshes itself, and navigates. They share the column layout and row
// rendering, which is the part worth not writing twice.

// uiRefreshInterval is how often the list re-reads while it is open.
//
// Two seconds rather than watch.go's 500ms: this is a background view of the
// whole table, not a close watch on one run, and every tick is a query
// against a table that grows forever. A run's state changing two seconds late
// on a list nobody is staring at costs nothing.
const uiRefreshInterval = 2 * time.Second

// uiPageLimit is how many runs one page holds. Set explicitly rather than
// left to the server's default, because the refresh merges against the first
// page only - a larger page means more of the table stays current per tick.
const uiPageLimit = 50

type runsRefreshedMsg struct{ page client.RunList }
type refreshTickMsg struct{}

func refreshTick() tea.Cmd {
	return tea.Tick(uiRefreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

type runsScreen struct {
	ctx    context.Context
	client *client.Client

	table      table.Model
	runs       []client.Run
	nextCursor *string
	loading    bool
	loaded     bool
	err        error
}

func newRunsScreen(ctx context.Context, c *client.Client) runsScreen {
	m := runsScreen{ctx: ctx, client: c}

	m.table = table.New(
		table.WithColumns(listColumns(defaultWidth)),
		table.WithFocused(true),
	)
	// SetStyles before SetHeight: bubbles/table derives its viewport height
	// from the *currently styled* header, and ours is two lines where the
	// default is one. Reversing these leaves a permanent blank row (the trap
	// that cost an hour in task 1.20).
	m.table.SetStyles(tableStyles())
	m.table.SetHeight(tableHeight(0))

	return m
}

func (m runsScreen) Init() tea.Cmd {
	return tea.Batch(m.fetchFirstPage(), refreshTick())
}

func (m runsScreen) Title() string { return "runs" }

func (m runsScreen) Help() string {
	help := "↑/↓ move · enter open · r refresh"
	if m.nextCursor != nil {
		help += " · scroll down for more"
	}
	return help
}

func (m runsScreen) fetchFirstPage() tea.Cmd {
	return func() tea.Msg {
		page, err := m.client.ListRuns(m.ctx, client.ListRunsParams{Limit: uiPageLimit})
		if err != nil {
			return errMsg{err}
		}
		return runsRefreshedMsg{page}
	}
}

func (m runsScreen) loadNextPage() tea.Cmd {
	if m.nextCursor == nil {
		return nil
	}
	cursor := *m.nextCursor

	return func() tea.Msg {
		page, err := m.client.ListRuns(m.ctx, client.ListRunsParams{
			Limit:  uiPageLimit,
			Cursor: cursor,
		})
		if err != nil {
			return errMsg{err}
		}
		return pageMsg{page}
	}
}

func (m runsScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.table.SetColumns(listColumns(msg.Width))
		m.table.SetHeight(min(tableHeight(len(m.runs)), max(msg.Height-chromeHeight, 3)))
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "left", "h":
			return m, popScreen()
		case "r":
			return m, m.fetchFirstPage()
		case "enter", "l", "right":
			if cursor := m.table.Cursor(); cursor >= 0 && cursor < len(m.runs) {
				return m, pushScreen(newDetailScreen(m.ctx, m.client, m.runs[cursor]))
			}
			return m, nil
		}

	case refreshTickMsg:
		// Refresh only the newest page, and merge it (see mergeFirstPage).
		// Re-fetching every loaded page on a timer would turn idle browsing
		// into unbounded query volume.
		return m, tea.Batch(m.fetchFirstPage(), refreshTick())

	case runsRefreshedMsg:
		m.loaded = true
		m.mergeFirstPage(msg.page)
		return m, nil

	case pageMsg:
		m.loading = false
		m.runs = append(m.runs, msg.page.Items...)
		m.nextCursor = msg.page.NextCursor
		m.syncTable()
		return m, nil

	case errMsg:
		// Not fatal here, unlike the one-shot list: a refresh that failed
		// once should show the last good table and try again on the next
		// tick, not tear the whole application down.
		m.err = msg.err
		return m, setStatus("refresh failed: %v", msg.err)
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	if m.table.Cursor() >= len(m.runs)-1 && m.nextCursor != nil && !m.loading {
		m.loading = true
		return m, tea.Batch(cmd, m.loadNextPage())
	}

	return m, cmd
}

// mergeFirstPage folds a fresh first page into the rows already on screen,
// rather than replacing them.
//
// Replacing would throw away every page the user had scrolled into and jump
// their cursor back to the top every two seconds. Merging works because runs
// are ordered by queued_at DESC: anything genuinely new belongs at the head,
// and anything already present has only changed state. So updates land in
// place, new runs are prepended, and the cursor stays where the user put it.
func (m *runsScreen) mergeFirstPage(page client.RunList) {
	m.err = nil

	known := make(map[int64]int, len(m.runs))
	for i, run := range m.runs {
		known[run.ID] = i
	}

	var fresh []client.Run
	for _, run := range page.Items {
		if i, ok := known[run.ID]; ok {
			m.runs[i] = run
			continue
		}
		fresh = append(fresh, run)
	}

	if len(fresh) > 0 {
		m.runs = append(fresh, m.runs...)
	}

	// The cursor only matters on the very first load, when there was no
	// page to page past yet.
	if m.nextCursor == nil && len(m.runs) == len(page.Items) {
		m.nextCursor = page.NextCursor
	}

	m.syncTable()
}

func (m *runsScreen) syncTable() {
	cursor := m.table.Cursor()
	m.table.SetRows(runRows(m.runs))
	m.table.SetHeight(tableHeight(len(m.runs)))
	// SetRows resets the cursor, which would fight the two-second refresh
	// for control of the selection.
	if cursor >= 0 && cursor < len(m.runs) {
		m.table.SetCursor(cursor)
	}
}

func (m runsScreen) View() string {
	if !m.loaded && m.err == nil {
		return styleHint.Render("  loading…") + "\n"
	}
	if len(m.runs) == 0 {
		return styleHint.Render("  no runs yet — start one from the menu") + "\n"
	}

	var b strings.Builder
	b.WriteString(m.table.View())
	b.WriteString("\n")
	if m.loading {
		b.WriteString(styleHint.Render("  loading more…"))
		b.WriteString("\n")
	}

	return b.String()
}

// --- Run detail ---

type detailScreen struct {
	ctx    context.Context
	client *client.Client
	run    client.Run

	cancelling bool
}

func newDetailScreen(ctx context.Context, c *client.Client, run client.Run) detailScreen {
	return detailScreen{ctx: ctx, client: c, run: run}
}

func (m detailScreen) Init() tea.Cmd {
	// A terminal run never changes again, so it needs no refresh loop at all.
	if m.run.IsTerminal() {
		return nil
	}
	return refreshTick()
}

func (m detailScreen) Title() string { return fmt.Sprintf("run %d", m.run.ID) }

func (m detailScreen) Help() string {
	help := "f follow output"
	if !m.run.IsTerminal() {
		help += " · c cancel"
	}
	return help + " · r refresh"
}

func (m detailScreen) fetch() tea.Cmd {
	return func() tea.Msg {
		run, err := m.client.GetRun(m.ctx, m.run.ID)
		if err != nil {
			return errMsg{err}
		}
		return runUpdateMsg{run}
	}
}

func (m detailScreen) cancel() tea.Cmd {
	return func() tea.Msg {
		run, err := m.client.CancelRun(m.ctx, m.run.ID)
		if err != nil {
			return errMsg{err}
		}
		return runUpdateMsg{run}
	}
}

func (m detailScreen) Update(msg tea.Msg) (uiScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "left", "h":
			return m, popScreen()
		case "f", "enter":
			return m, pushScreen(newLogsScreen(m.ctx, m.client, m.run.ID))
		case "r":
			return m, m.fetch()
		case "c":
			if m.run.IsTerminal() || m.cancelling {
				return m, nil
			}
			m.cancelling = true
			// The status says "requested", not "cancelled", because that is
			// all that has happened: for a running run the API returns 202
			// and the supervisor still has to stop the container (task 2.8).
			return m, tea.Batch(m.cancel(), setStatus("cancellation requested for run %d", m.run.ID))
		}

	case refreshTickMsg:
		if m.run.IsTerminal() {
			return m, nil
		}
		return m, tea.Batch(m.fetch(), refreshTick())

	case runUpdateMsg:
		m.run = msg.run
		if m.run.IsTerminal() && m.cancelling {
			m.cancelling = false
			return m, setStatus("run %d is now %s", m.run.ID, m.run.State)
		}
		return m, nil

	case errMsg:
		m.cancelling = false
		return m, setStatus("%v", msg.err)
	}

	return m, nil
}

func (m detailScreen) View() string {
	view := renderRunSummary(m.run, false)

	if m.cancelling && !m.run.IsTerminal() {
		view += styleHint.Render("  waiting for the supervisor to stop the container…") + "\n"
	}

	return view
}
