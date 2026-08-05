// Package systemdunit writes and reloads the generated systemd (user)
// .timer/.service unit pairs schedules render into (internal/scheduling),
// per ARCHITECTURE.md §4.8 (decision #27, task 5.3). The supervisor is the
// only caller - the api process never touches SYSTEMD_UNIT_DIR or shells
// out to systemctl, mirroring how it is the sole writer of RUN_LOG_DIR
// (CLAUDE.md's invariants).
package systemdunit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Manager writes unit files under Dir and drives them through `systemctl
// --user`. Dir is expected to already exist (task 5.3's default,
// $HOME/.config/systemd/user, is created by systemd itself the first time a
// user unit is installed by any means - but an operator-supplied
// SYSTEMD_UNIT_DIR might not exist yet, so Write creates it if missing).
type Manager struct {
	Dir string
}

func NewManager(dir string) *Manager {
	return &Manager{Dir: dir}
}

func (m *Manager) timerPath(stem string) string {
	return filepath.Join(m.Dir, stem+".timer")
}

func (m *Manager) servicePath(stem string) string {
	return filepath.Join(m.Dir, stem+".service")
}

// Write writes the .timer and .service files for stem if their content
// differs from what's already on disk, and reports whether anything
// changed - the caller uses this to decide whether a daemon-reload is
// actually needed, rather than reloading on every poll tick regardless.
func (m *Manager) Write(stem, timerUnit, serviceUnit string) (changed bool, err error) {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return false, fmt.Errorf("systemdunit: creating %s: %w", m.Dir, err)
	}

	timerChanged, err := writeIfDifferent(m.timerPath(stem), timerUnit)
	if err != nil {
		return false, err
	}
	serviceChanged, err := writeIfDifferent(m.servicePath(stem), serviceUnit)
	if err != nil {
		return false, err
	}
	return timerChanged || serviceChanged, nil
}

func writeIfDifferent(path, content string) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("systemdunit: reading %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("systemdunit: writing %s: %w", path, err)
	}
	return true, nil
}

// Remove deletes stem's unit files, ignoring an already-missing file - the
// supervisor calls this for schedule ids no longer present in Postgres, and
// a partially-cleaned-up state (e.g. a previous Remove that reloaded but
// didn't get to delete both files) must not be an error on retry.
func (m *Manager) Remove(stem string) (removed bool, err error) {
	timerRemoved, err := removeIfExists(m.timerPath(stem))
	if err != nil {
		return false, err
	}
	serviceRemoved, err := removeIfExists(m.servicePath(stem))
	if err != nil {
		return false, err
	}
	return timerRemoved || serviceRemoved, nil
}

func removeIfExists(path string) (bool, error) {
	err := os.Remove(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("systemdunit: removing %s: %w", path, err)
}

// ListScheduleIDs returns the schedule ids with a unit pair currently on
// disk, parsed from scheduling.UnitFileStem's naming convention - the
// supervisor's schedule-sync loop uses this to find stray units for
// schedules that no longer exist in Postgres.
func (m *Manager) ListScheduleIDs() ([]int64, error) {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("systemdunit: reading %s: %w", m.Dir, err)
	}

	seen := make(map[int64]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "descendence-schedule-") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".timer"), ".service")
		idStr := strings.TrimPrefix(stem, "descendence-schedule-")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue // not one of ours (or malformed) - skip rather than fail the whole sweep
		}
		seen[id] = true
	}

	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

// Reload runs `systemctl --user daemon-reload`, required before a newly
// written or removed unit file is recognized.
func (m *Manager) Reload(ctx context.Context) error {
	return runSystemctl(ctx, "daemon-reload")
}

// EnableNow enables and starts the timer for stem - schedules.enabled ==
// true.
func (m *Manager) EnableNow(ctx context.Context, stem string) error {
	return runSystemctl(ctx, "enable", "--now", stem+".timer")
}

// DisableNow disables and stops the timer for stem - schedules.enabled ==
// false. The unit files stay on disk (only Remove deletes them); a disabled
// schedule is still a schedule, just not currently firing.
func (m *Manager) DisableNow(ctx context.Context, stem string) error {
	return runSystemctl(ctx, "disable", "--now", stem+".timer")
}

func runSystemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemdunit: systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
