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

func testRuns(ids ...int64) []client.Run {
	runs := make([]client.Run, len(ids))
	for i, id := range ids {
		runs[i] = client.Run{
			ID:       id,
			State:    client.StateSucceeded,
			ImageRef: "docker.io/library/alpine:latest",
			Argv:     []string{"echo", "hi"},
			QueuedAt: time.Now().Add(-time.Minute),
		}
	}
	return runs
}

func testListModel(runs []client.Run, nextCursor *string) listModel {
	page := client.RunList{Items: runs, NextCursor: nextCursor}
	return newListModel(context.Background(), client.New("http://example.invalid", "t"), page, 0)
}

// key builds a KeyMsg from a name. Shared by every test in this package that
// drives an Update; named keys have their own KeyType, anything else is typed
// runes.
func key(s string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter":     tea.KeyEnter,
		"esc":       tea.KeyEsc,
		"tab":       tea.KeyTab,
		"up":        tea.KeyUp,
		"down":      tea.KeyDown,
		"left":      tea.KeyLeft,
		"right":     tea.KeyRight,
		"home":      tea.KeyHome,
		"end":       tea.KeyEnd,
		"backspace": tea.KeyBackspace,
		"ctrl+c":    tea.KeyCtrlC,
	}

	if keyType, ok := named[s]; ok {
		return tea.KeyMsg{Type: keyType}
	}

	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Enter opens the highlighted run, and the final view is the same summary
// block `descendence run` and `runs get` produce - a run should look
// identical however you arrived at it.
func TestListEnterOpensHighlightedRun(t *testing.T) {
	m := testListModel(testRuns(11, 12, 13), nil)

	updated, cmd := m.Update(key("enter"))
	if !quits(t, cmd) {
		t.Fatal("enter did not exit the list")
	}

	final := updated.(listModel)
	if final.chosen == nil {
		t.Fatal("enter selected no run")
	}
	if final.chosen.ID != 11 {
		t.Errorf("opened run %d, want the highlighted one (11)", final.chosen.ID)
	}
	if view := final.View(); !strings.Contains(view, "run 11") {
		t.Errorf("final view is not the run summary:\n%s", view)
	}
}

func TestListQuitsWithoutChoosing(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := testListModel(testRuns(1, 2), nil)

		msg := key(k)
		if k == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		}

		updated, cmd := m.Update(msg)
		if !quits(t, cmd) {
			t.Fatalf("%q did not exit the list", k)
		}
		if updated.(listModel).chosen != nil {
			t.Errorf("%q opened a run; it should just quit", k)
		}
	}
}

// Reaching the bottom pulls in the next page, so the keyset cursor never
// has to be shown to the user - which is the whole point of it being
// opaque.
func TestListLoadsNextPageAtTheBottom(t *testing.T) {
	cursor := "opaque-cursor"
	m := testListModel(testRuns(5, 4, 3), &cursor)

	model := tea.Model(m)

	// Walk down until the fetch is triggered, and keep the command from
	// that step - a later keypress at an already-loading bottom row
	// correctly issues nothing.
	var fetch tea.Cmd
	for range 3 {
		next, cmd := model.Update(key("down"))
		model = next
		if model.(listModel).loading {
			fetch = cmd
			break
		}
	}

	if !model.(listModel).loading {
		t.Fatal("reaching the last row did not start loading the next page")
	}
	if fetch == nil {
		t.Fatal("no command issued to fetch the next page")
	}
}

// The bottom row must not re-trigger a fetch while one is already in
// flight, or scrolling would fire a request per keypress.
func TestListDoesNotRefetchWhileLoading(t *testing.T) {
	cursor := "opaque-cursor"
	m := testListModel(testRuns(5, 4), &cursor)
	m.loading = true

	_, cmd := m.Update(key("down"))
	if cmd != nil {
		t.Error("issued a second fetch while one was already in flight")
	}
}

func TestListNoFetchWithoutACursor(t *testing.T) {
	m := testListModel(testRuns(5, 4, 3), nil)

	model := tea.Model(m)
	for range 3 {
		model, _ = model.Update(key("down"))
	}

	if model.(listModel).loading {
		t.Error("tried to load another page even though this was the last one")
	}
}

func TestListAppendsFetchedPage(t *testing.T) {
	cursor := "opaque-cursor"
	m := testListModel(testRuns(5, 4), &cursor)

	updated, _ := m.Update(pageMsg{client.RunList{Items: testRuns(3, 2), NextCursor: nil}})

	final := updated.(listModel)
	if len(final.runs) != 4 {
		t.Fatalf("have %d runs after a second page, want 4", len(final.runs))
	}
	if final.runs[2].ID != 3 {
		t.Errorf("page 2 was not appended in order: got run %d at index 2", final.runs[2].ID)
	}
	if final.nextCursor != nil {
		t.Error("cursor was not cleared on the last page; the list would refetch forever")
	}
	if final.loading {
		t.Error("still marked loading after the page arrived")
	}
}

func TestListQuitsOnFetchError(t *testing.T) {
	m := testListModel(testRuns(1), nil)

	updated, cmd := m.Update(errMsg{errors.New("connection refused")})
	if !quits(t, cmd) {
		t.Fatal("a fetch error did not exit the list")
	}
	if view := updated.(listModel).View(); !strings.Contains(view, "connection refused") {
		t.Errorf("view does not surface the error:\n%s", view)
	}
}

// The layout must stay inside the terminal at any width - a table that
// wraps is unreadable, and the two flexible columns are the ones that can
// push it over.
func TestListColumnsFitTheTerminal(t *testing.T) {
	for _, width := range []int{40, 80, 100, 150, 240} {
		cols := listColumns(width)

		total := 0
		for _, col := range cols {
			if col.Width < 1 {
				t.Errorf("width %d: column %q collapsed to %d", width, col.Title, col.Width)
			}
			total += col.Width + 2 // bubbles/table pads each cell by one column per side
		}

		// Narrow terminals can't be satisfied; the columns clamp to a
		// readable minimum instead and the table scrolls sideways.
		if width >= 100 && total > width {
			t.Errorf("width %d: columns total %d, which overflows", width, total)
		}
	}
}

// bubbles/table's SetHeight is a *total* including the header, and our
// header is two lines rather than the default one. Getting this wrong
// leaves a permanent blank row at the bottom of the table.
func TestTableHeightAccountsForTheHeader(t *testing.T) {
	if got, want := tableHeight(6), 6+headerHeight; got != want {
		t.Errorf("tableHeight(6) = %d, want %d", got, want)
	}
	if got := tableHeight(0); got != 1+headerHeight {
		t.Errorf("tableHeight(0) = %d, want an empty table to still be valid", got)
	}
	if got, want := tableHeight(500), maxTableRows+headerHeight; got != want {
		t.Errorf("tableHeight(500) = %d, want it capped at %d", got, want)
	}
}

func TestExitCodeTextDistinguishesZeroFromAbsent(t *testing.T) {
	zero := int32(0)

	if got := exitCodeText(client.Run{ExitCode: &zero}); got != "0" {
		t.Errorf("exit code 0 rendered as %q, want \"0\"", got)
	}
	if got := exitCodeText(client.Run{}); got != "-" {
		t.Errorf("a run with no exit code rendered as %q, want \"-\"", got)
	}
}

func TestRunDuration(t *testing.T) {
	started := time.Now().Add(-10 * time.Second)
	finished := started.Add(4 * time.Second)

	if got := runDuration(client.Run{}); got != "-" {
		t.Errorf("queued run duration = %q, want \"-\"", got)
	}
	if got := runDuration(client.Run{StartedAt: &started, FinishedAt: &finished}); got != "4.0s" {
		t.Errorf("finished run duration = %q, want \"4.0s\"", got)
	}
	// A still-running run is measured against now, so it must be at least
	// as long as it has actually been going.
	if got := runDuration(client.Run{StartedAt: &started}); got != "10.0s" {
		t.Errorf("running run duration = %q, want \"10.0s\"", got)
	}
}

func TestFormatRelative(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		4 * time.Minute:  "4m ago",
		3 * time.Hour:    "3h ago",
		50 * time.Hour:   "2d ago",
	}

	for ago, want := range cases {
		if got := formatRelative(time.Now().Add(-ago)); got != want {
			t.Errorf("formatRelative(%s ago) = %q, want %q", ago, got, want)
		}
	}
}
