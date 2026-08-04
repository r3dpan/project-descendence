package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/r3dpan/project-descendence/internal/client"
)

// pageMsg carries one fetched page of runs.
type pageMsg struct{ page client.RunList }

// listModel is a browsable table of runs. Further pages load as the cursor
// reaches the bottom, so the keyset cursor never has to be exposed to the
// user - which is the point of it being opaque.
type listModel struct {
	ctx    context.Context
	client *client.Client
	limit  int32

	table      table.Model
	runs       []client.Run
	nextCursor *string
	loading    bool

	// chosen is the run the user pressed enter on. Set only on the way out;
	// the final view renders it in full instead of the table.
	chosen *client.Run
	err    error
}

func newListModel(ctx context.Context, c *client.Client, page client.RunList, limit int32) listModel {
	m := listModel{
		ctx:        ctx,
		client:     c,
		limit:      limit,
		runs:       page.Items,
		nextCursor: page.NextCursor,
	}

	m.table = table.New(
		table.WithColumns(listColumns(defaultWidth)),
		table.WithRows(runRows(page.Items)),
		table.WithFocused(true),
	)
	// Order matters: bubbles/table derives its viewport height by measuring
	// the *currently styled* header, and our header is two lines (titles
	// plus a rule) where the default is one. Setting the height before the
	// styles would leave a stray blank row at the bottom forever.
	m.table.SetStyles(tableStyles())
	m.table.SetHeight(tableHeight(len(page.Items)))

	return m
}

// defaultWidth is used until the first WindowSizeMsg arrives. Anything
// narrower than this and the table would be unreadable anyway.
const defaultWidth = 100

const (
	// maxTableRows bounds the visible rows so the table scrolls internally
	// rather than pushing the whole terminal around.
	maxTableRows = 20
	// headerHeight is what bubbles/table's own header occupies (titles plus
	// the rule under them). SetHeight takes the *total* height including it,
	// so it has to be added back or the last rows get clipped.
	headerHeight = 2
)

func tableHeight(rows int) int {
	visible := min(rows, maxTableRows)
	if visible < 1 {
		visible = 1
	}
	return visible + headerHeight
}

// listColumns lays out the table for a given terminal width. The fixed
// columns take what they need; image and argv split whatever is left,
// because those two are genuinely unbounded. Argv gets the larger share
// and image is capped - past a point an image reference is just a long
// registry prefix, whereas argv is what you are actually scanning for.
func listColumns(width int) []table.Column {
	const (
		idW       = 6
		stateW    = 10
		exitW     = 5
		queuedW   = 10
		durationW = 9
		// bubbles/table pads every cell by one column on each side.
		padding     = 2 * 7
		minFlexible = 12
		maxImageW   = 44
	)

	remaining := max(width-(idW+stateW+exitW+queuedW+durationW+padding), 2*minFlexible)

	imageW := min(max(remaining*2/5, minFlexible), maxImageW)
	argvW := max(remaining-imageW, minFlexible)

	return []table.Column{
		{Title: "ID", Width: idW},
		{Title: "STATE", Width: stateW},
		{Title: "EXIT", Width: exitW},
		{Title: "QUEUED", Width: queuedW},
		{Title: "DURATION", Width: durationW},
		{Title: "IMAGE", Width: imageW},
		{Title: "ARGV", Width: argvW},
	}
}

func runRows(runs []client.Run) []table.Row {
	rows := make([]table.Row, len(runs))
	for i, run := range runs {
		rows[i] = table.Row{
			strconv.FormatInt(run.ID, 10),
			run.State,
			exitCodeText(run),
			formatRelative(run.QueuedAt),
			runDuration(run),
			run.ImageRef,
			strings.Join(run.Argv, " "),
		}
	}
	return rows
}

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.AdaptiveColor{Light: "231", Dark: "231"}).
		Background(lipgloss.AdaptiveColor{Light: "26", Dark: "62"}).
		Bold(true)
	return s
}

func (m listModel) Init() tea.Cmd { return nil }

// loadNextPage fetches the page after the current cursor. One page per
// command; the next is only requested once this one lands.
func (m listModel) loadNextPage() tea.Cmd {
	if m.nextCursor == nil {
		return nil
	}
	cursor := *m.nextCursor

	return func() tea.Msg {
		page, err := m.client.ListRuns(m.ctx, client.ListRunsParams{
			Limit:  m.limit,
			Cursor: cursor,
		})
		if err != nil {
			return errMsg{err}
		}
		return pageMsg{page}
	}
}

func (m listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.table.SetColumns(listColumns(msg.Width))
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "enter":
			if cursor := m.table.Cursor(); cursor >= 0 && cursor < len(m.runs) {
				run := m.runs[cursor]
				m.chosen = &run
			}
			return m, tea.Quit
		}

	case pageMsg:
		m.loading = false
		m.runs = append(m.runs, msg.page.Items...)
		m.nextCursor = msg.page.NextCursor
		m.table.SetRows(runRows(m.runs))
		m.table.SetHeight(tableHeight(len(m.runs)))
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	// Infinite scroll: reaching the last row pulls the next page in, so the
	// opaque cursor stays an implementation detail.
	if m.table.Cursor() >= len(m.runs)-1 && m.nextCursor != nil && !m.loading {
		m.loading = true
		return m, tea.Batch(cmd, m.loadNextPage())
	}

	return m, cmd
}

func (m listModel) View() string {
	if m.err != nil {
		return styleError.Render("error: ") + m.err.Error() + "\n"
	}
	if m.chosen != nil {
		return renderRunSummary(*m.chosen, false)
	}

	var b strings.Builder
	b.WriteString(m.table.View())
	b.WriteString("\n")

	hint := "↑/↓ move · enter to open · q to quit"
	if m.loading {
		hint = "loading more…"
	} else if m.nextCursor != nil {
		hint = "↑/↓ move · enter to open · scroll to the bottom for more · q to quit"
	}
	b.WriteString(styleHint.Render("  " + hint))
	b.WriteString("\n")

	return b.String()
}

// listInteractive runs the table, then leaves whatever the final view was -
// the table itself, or one run in full if the user opened it - in the
// scrollback, so quitting doesn't wipe the answer off the screen.
func listInteractive(ctx context.Context, c *client.Client, page client.RunList, limit int32) int {
	model, err := tea.NewProgram(newListModel(ctx, c, page, limit)).Run()
	if err != nil {
		printError(err)
		return 1
	}

	if final := model.(listModel); final.err != nil {
		return 1
	}

	return 0
}
