package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

// Update and View are pure functions of (model, message), so they are tested
// directly rather than by driving a terminal. That is not a shortcut - task
// 1.20 established it after a hand-rolled pty harness hung, because lipgloss
// asks the terminal for its background colour on startup and a harness has no
// answer. See HISTORY.md.

// msgOf runs a command and returns the message it produced, so a test can
// assert on navigation without a running program.
func msgOf(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func testMenu() menuScreen {
	return newMenuScreen(context.Background(), client.New("http://127.0.0.1:0", "t"), config{}, "admin")
}

func TestMenuNavigation(t *testing.T) {
	m := uiScreen(testMenu())

	// Cursor stops at the top rather than wrapping: in a short fixed list,
	// wrapping means "up" from the first entry silently jumps to the last.
	m, _ = m.Update(key("up"))
	if got := m.(menuScreen).cursor; got != 0 {
		t.Errorf("cursor = %d after up at the top, want 0", got)
	}

	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("down"))
	if got := m.(menuScreen).cursor; got != 2 {
		t.Errorf("cursor = %d after two downs, want 2", got)
	}

	entries := len(m.(menuScreen).entries)
	for i := 0; i < entries+3; i++ {
		m, _ = m.Update(key("down"))
	}
	if got := m.(menuScreen).cursor; got != entries-1 {
		t.Errorf("cursor = %d after running off the bottom, want %d", got, entries-1)
	}
}

func TestMenuEnterOpensAScreen(t *testing.T) {
	m := uiScreen(testMenu())

	_, cmd := m.Update(key("enter"))
	if _, ok := msgOf(cmd).(pushScreenMsg); !ok {
		t.Fatalf("enter on the first entry produced %T, want pushScreenMsg", msgOf(cmd))
	}
}

// Leaving the root menu leaves the application; the root turns a pop of the
// last screen into a quit.
func TestMenuEscPops(t *testing.T) {
	m := uiScreen(testMenu())

	_, cmd := m.Update(key("esc"))
	if _, ok := msgOf(cmd).(popScreenMsg); !ok {
		t.Fatalf("esc produced %T, want popScreenMsg", msgOf(cmd))
	}
}

func testRootModel() uiModel {
	return newUIModel(context.Background(), client.New("http://127.0.0.1:0", "t"), config{}, "admin")
}

func TestRootPushAndPop(t *testing.T) {
	root := testRootModel()
	if got := len(root.stack); got != 1 {
		t.Fatalf("a new model has %d screens, want 1", got)
	}

	pushed, _ := root.Update(pushScreenMsg{newConfigScreen(config{})})
	root = pushed.(uiModel)
	if got := len(root.stack); got != 2 {
		t.Fatalf("stack is %d deep after a push, want 2", got)
	}
	if got := root.top().Title(); got != "configuration" {
		t.Errorf("top screen is %q, want the pushed one", got)
	}

	popped, _ := root.Update(popScreenMsg{})
	root = popped.(uiModel)
	if got := len(root.stack); got != 1 {
		t.Errorf("stack is %d deep after a pop, want 1", got)
	}
}

// Popping the last screen is how the application exits, so it must quit
// rather than leaving an empty stack for View to index into.
func TestRootPopOfLastScreenQuits(t *testing.T) {
	root := testRootModel()

	_, cmd := root.Update(popScreenMsg{})
	if msgOf(cmd) != tea.Quit() {
		t.Error("popping the only screen did not quit")
	}
}

// An interactive application you cannot reliably leave is worse than none, so
// ctrl+c is intercepted at the root and never reaches a screen - including a
// screen with a focused text field, which would otherwise swallow it.
func TestRootCtrlCAlwaysQuits(t *testing.T) {
	root := testRootModel()
	pushed, _ := root.Update(pushScreenMsg{newFormScreen(context.Background(), nil)})
	root = pushed.(uiModel)

	_, cmd := root.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if msgOf(cmd) != tea.Quit() {
		t.Error("ctrl+c inside a form did not quit")
	}
}

// A failed request must not tear the application down. The root forwards
// errMsg to the screen instead of handling it, so an intermittent refresh
// failure shows a status line and the next tick retries - the first version
// of this quit instead, which would have dropped the user back to their shell
// the first time the API hiccuped.
func TestRootDoesNotQuitOnAnError(t *testing.T) {
	root := testRootModel()
	pushed, _ := root.Update(pushScreenMsg{newRunsScreen(context.Background(), nil)})
	root = pushed.(uiModel)

	updated, cmd := root.Update(errMsg{err: context.DeadlineExceeded})
	if msgOf(cmd) == tea.Quit() {
		t.Fatal("an errMsg quit the application")
	}
	if got := len(updated.(uiModel).stack); got != 2 {
		t.Errorf("stack is %d deep after an error, want it untouched at 2", got)
	}
}

// --- The new-run form ---

// The invariant this whole project is careful about (task 1.11): argv is an
// array, never a shell string. A single text field is a natural way to type a
// command and a natural way to smuggle one, so what the field produces is
// worth pinning down.
func TestFormArgvIsAnArrayNeverAShellString(t *testing.T) {
	cases := []struct {
		typed string
		want  []string
	}{
		{"echo hello", []string{"echo", "hello"}},
		{"  echo   spaced   out  ", []string{"echo", "spaced", "out"}},
		// The one that matters: metacharacters stay literal argv elements.
		// Nothing here or on the server ever hands them to a shell.
		{"echo hi; rm -rf /", []string{"echo", "hi;", "rm", "-rf", "/"}},
		{"sh -c echo", []string{"sh", "-c", "echo"}},
		{"a|b c&&d", []string{"a|b", "c&&d"}},
	}

	for _, tc := range cases {
		m := newFormScreen(context.Background(), nil)
		m.inputs[fieldImage].SetValue("alpine")
		m.inputs[fieldArgv].SetValue(tc.typed)

		params, err := m.params()
		if err != nil {
			t.Fatalf("params() for %q: %v", tc.typed, err)
		}

		if len(params.Argv) != len(tc.want) {
			t.Errorf("%q produced %d argv elements %q, want %d %q",
				tc.typed, len(params.Argv), params.Argv, len(tc.want), tc.want)
			continue
		}
		for i := range tc.want {
			if params.Argv[i] != tc.want[i] {
				t.Errorf("%q argv[%d] = %q, want %q", tc.typed, i, params.Argv[i], tc.want[i])
			}
		}
	}
}

func TestFormValidation(t *testing.T) {
	cases := []struct {
		name    string
		image   string
		argv    string
		timeout string
		wantErr string
	}{
		{name: "no image", argv: "true", wantErr: "image"},
		{name: "no command", image: "alpine", wantErr: "command"},
		{name: "blank command", image: "alpine", argv: "   ", wantErr: "command"},
		{name: "timeout not a number", image: "alpine", argv: "true", timeout: "soon", wantErr: "timeout"},
		{name: "timeout zero", image: "alpine", argv: "true", timeout: "0", wantErr: "timeout"},
		{name: "timeout negative", image: "alpine", argv: "true", timeout: "-5", wantErr: "timeout"},
		{name: "valid", image: "alpine", argv: "true", timeout: "30"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormScreen(context.Background(), nil)
			m.inputs[fieldImage].SetValue(tc.image)
			m.inputs[fieldArgv].SetValue(tc.argv)
			m.inputs[fieldTimeout].SetValue(tc.timeout)

			params, err := m.params()

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("params() = %v, want success", err)
				}
				if params.TimeoutSeconds != 30 {
					t.Errorf("timeout = %d, want 30", params.TimeoutSeconds)
				}
				return
			}

			if err == nil {
				t.Fatalf("params() succeeded, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// An empty timeout means "let the server decide" rather than zero, which the
// API would reject.
func TestFormEmptyTimeoutIsUnset(t *testing.T) {
	m := newFormScreen(context.Background(), nil)
	m.inputs[fieldImage].SetValue("alpine")
	m.inputs[fieldArgv].SetValue("true")

	params, err := m.params()
	if err != nil {
		t.Fatalf("params(): %v", err)
	}
	if params.TimeoutSeconds != 0 {
		t.Errorf("timeout = %d, want 0 so the client omits it", params.TimeoutSeconds)
	}
}

func TestFormTabCyclesFields(t *testing.T) {
	m := newFormScreen(context.Background(), nil)

	for i := 0; i < fieldCount; i++ {
		if m.focused != i {
			t.Fatalf("focus = %d, want %d", m.focused, i)
		}
		if !m.inputs[i].Focused() {
			t.Errorf("field %d is not focused but the model says it is", i)
		}
		next, _ := m.Update(key("tab"))
		m = next.(formScreen)
	}

	if m.focused != 0 {
		t.Errorf("focus = %d after cycling every field, want it wrapped to 0", m.focused)
	}
}

// --- The runs list ---

func testRun(id int64, state string) client.Run {
	return client.Run{ID: id, State: state, ImageRef: "alpine", Argv: []string{"true"}}
}

// The refresh merges rather than replaces. Replacing every two seconds would
// throw away pages the user had scrolled into and reset their cursor.
func TestRunsMergeUpdatesInPlaceAndPrepends(t *testing.T) {
	m := newRunsScreen(context.Background(), nil)
	m.mergeFirstPage(client.RunList{Items: []client.Run{
		testRun(3, client.StateRunning), testRun(2, client.StateSucceeded), testRun(1, client.StateSucceeded),
	}})

	if got := len(m.runs); got != 3 {
		t.Fatalf("%d runs after the first page, want 3", got)
	}

	// Run 3 finished, and run 4 is new. Newest first, so 4 belongs at the head.
	m.mergeFirstPage(client.RunList{Items: []client.Run{
		testRun(4, client.StateQueued), testRun(3, client.StateSucceeded), testRun(2, client.StateSucceeded),
	}})

	if got := len(m.runs); got != 4 {
		t.Fatalf("%d runs after the refresh, want 4 - a merge must not duplicate", got)
	}
	if m.runs[0].ID != 4 {
		t.Errorf("head is run %d, want the new run 4 prepended", m.runs[0].ID)
	}
	if m.runs[1].ID != 3 || m.runs[1].State != client.StateSucceeded {
		t.Errorf("run 3 = %+v, want its state updated in place", m.runs[1])
	}
	if m.runs[3].ID != 1 {
		t.Errorf("tail is run %d, want the older page kept at 1", m.runs[3].ID)
	}
}

// The cursor must survive a refresh, or the two-second tick fights the user
// for control of the selection.
func TestRunsRefreshKeepsTheCursor(t *testing.T) {
	m := newRunsScreen(context.Background(), nil)
	m.mergeFirstPage(client.RunList{Items: []client.Run{
		testRun(5, client.StateRunning), testRun(4, client.StateRunning), testRun(3, client.StateRunning),
	}})

	m.table.SetCursor(2)
	m.mergeFirstPage(client.RunList{Items: []client.Run{
		testRun(5, client.StateSucceeded), testRun(4, client.StateRunning), testRun(3, client.StateRunning),
	}})

	if got := m.table.Cursor(); got != 2 {
		t.Errorf("cursor = %d after a refresh, want it left at 2", got)
	}
}

// --- The log viewer ---

func TestLogsFollowPausesWhenScrolledAway(t *testing.T) {
	m := newLogsScreen(context.Background(), client.New("http://127.0.0.1:0", "t"), 1)
	defer m.cancel()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(*logsScreen)

	if !m.following {
		t.Fatal("a new viewer does not start in following mode")
	}

	// More lines than fit, so there is somewhere to scroll to.
	for i := 1; i <= 100; i++ {
		next, _ := m.Update(logLineMsg(client.LogLine{Seq: int64(i), Stream: "stdout", Text: "line"}))
		m = next.(*logsScreen)
	}

	next, _ := m.Update(key("home"))
	m = next.(*logsScreen)
	if m.following {
		t.Error("still following after jumping to the top; new output would yank the reader back down")
	}

	next, _ = m.Update(key("end"))
	m = next.(*logsScreen)
	if !m.following {
		t.Error("not following again after returning to the bottom")
	}
}

// Leaving the viewer must stop the stream. Otherwise the follow goroutine and
// its HTTP connection outlive the screen for the rest of the run - the
// client-side version of the leak task 2.7 fixed on the server.
func TestLogsEscStopsTheStream(t *testing.T) {
	m := newLogsScreen(context.Background(), client.New("http://127.0.0.1:0", "t"), 1)

	_, cmd := m.Update(key("esc"))
	if _, ok := msgOf(cmd).(popScreenMsg); !ok {
		t.Fatalf("esc produced %T, want popScreenMsg", msgOf(cmd))
	}

	select {
	case <-m.ctx.Done():
	default:
		t.Error("the stream context is still live after leaving the screen")
	}
}

func TestRunPickerAcceptsOnlyDigits(t *testing.T) {
	m := uiScreen(newRunPickerScreen(context.Background(), nil))

	for _, k := range []string{"4", "a", "2", "-", "!", "7"} {
		m, _ = m.Update(key(k))
	}

	if got := m.(runPickerScreen).input; got != "427" {
		t.Errorf("input = %q, want %q - non-digits should never enter the field", got, "427")
	}

	m, _ = m.Update(key("backspace"))
	if got := m.(runPickerScreen).input; got != "42" {
		t.Errorf("input = %q after backspace, want %q", got, "42")
	}
}

func TestRunPickerRejectsAnEmptyID(t *testing.T) {
	m := uiScreen(newRunPickerScreen(context.Background(), nil))

	m, cmd := m.Update(key("enter"))
	if _, ok := msgOf(cmd).(pushScreenMsg); ok {
		t.Fatal("enter on an empty field opened a log viewer")
	}
	if m.(runPickerScreen).err == "" {
		t.Error("enter on an empty field reported nothing")
	}
}
