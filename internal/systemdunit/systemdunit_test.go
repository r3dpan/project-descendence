package systemdunit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newTestManager points at a throwaway directory and skips cleanly if
// systemctl --user isn't reachable in this environment, matching the
// connect-or-skip pattern internal/podman's tests already use.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available")
	}
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		t.Skipf("systemctl --user not reachable: %v", err)
	}
	return NewManager(t.TempDir())
}

func TestWriteIsIdempotentAndReportsChange(t *testing.T) {
	m := newTestManager(t)
	stem := "descendence-schedule-999999"

	changed, err := m.Write(stem, "timer content\n", "service content\n")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !changed {
		t.Fatal("first Write should report changed=true")
	}

	changed, err = m.Write(stem, "timer content\n", "service content\n")
	if err != nil {
		t.Fatalf("Write (repeat): %v", err)
	}
	if changed {
		t.Fatal("repeating an identical Write should report changed=false")
	}

	changed, err = m.Write(stem, "different timer content\n", "service content\n")
	if err != nil {
		t.Fatalf("Write (modified): %v", err)
	}
	if !changed {
		t.Fatal("Write with different content should report changed=true")
	}
}

func TestListScheduleIDsAndRemove(t *testing.T) {
	m := newTestManager(t)

	if _, err := m.Write("descendence-schedule-1", "t\n", "s\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := m.Write("descendence-schedule-42", "t\n", "s\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A file that doesn't match the naming convention should be ignored,
	// not crash the sweep.
	if err := os.WriteFile(filepath.Join(m.Dir, "unrelated.timer"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing unrelated file: %v", err)
	}

	ids, err := m.ListScheduleIDs()
	if err != nil {
		t.Fatalf("ListScheduleIDs: %v", err)
	}
	got := map[int64]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[1] || !got[42] || len(got) != 2 {
		t.Fatalf("ListScheduleIDs = %v, want exactly [1 42]", ids)
	}

	removed, err := m.Remove("descendence-schedule-1")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatal("Remove of an existing unit pair should report removed=true")
	}

	removed, err = m.Remove("descendence-schedule-1")
	if err != nil {
		t.Fatalf("Remove (already gone): %v", err)
	}
	if removed {
		t.Fatal("Remove of an already-missing unit pair should report removed=false, not error")
	}

	ids, err = m.ListScheduleIDs()
	if err != nil {
		t.Fatalf("ListScheduleIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("ListScheduleIDs after Remove = %v, want [42]", ids)
	}
}

func TestReload(t *testing.T) {
	m := newTestManager(t)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}
