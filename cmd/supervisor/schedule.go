package main

import (
	"context"
	"log"
	"time"

	"github.com/r3dpan/project-descendence/internal/scheduling"
	"github.com/r3dpan/project-descendence/internal/store"
	"github.com/r3dpan/project-descendence/internal/systemdunit"
)

// scheduleSyncInterval: schedule changes are rare and low-urgency compared
// to a queued run, so this polls far less often than pollInterval - the
// propagation delay this accepts (a schedule CRUD write to its unit
// actually existing) is decision #27's documented trade-off for keeping the
// api process's schedule writes a plain Postgres write.
const scheduleSyncInterval = 5 * time.Second

// runScheduleSyncLoop is runClaimLoop's structural twin (see build.go's
// comment on why a third, separate, structurally-similar loop is preferred
// here over a shared abstraction) over schedules instead of runs: no
// container to wait on, no claim - just "does the generated unit for each
// schedule row match what's on disk", ticking on scheduleSyncInterval
// rather than pollInterval. Runs under the same advisory lock acquired in
// main(), which is what guarantees exactly one process ever touches
// SYSTEMD_UNIT_DIR (ARCHITECTURE.md §4.8, decision #27) - not a second
// locking primitive.
func runScheduleSyncLoop(ctx context.Context, queries *store.Queries, unitMgr *systemdunit.Manager, cliPath, tokenFile string) {
	ticker := time.NewTicker(scheduleSyncInterval)
	defer ticker.Stop()

	for {
		// force=false: only schedules whose rendered unit content actually
		// changed get an enable/disable call - see syncSchedules' comment.
		syncSchedules(ctx, queries, unitMgr, cliPath, tokenFile, false)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// syncSchedules reconciles every schedules row against the unit files on
// disk: writes/updates a unit pair for each row, removes unit pairs for ids
// no longer present, and reloads systemd exactly once if anything changed -
// never per schedule, since a daemon-reload is one process-wide operation.
//
// force controls whether EnableNow/DisableNow runs even for a schedule
// whose unit content didn't change this tick. A unit's enabled/disabled
// state (a systemd symlink) is not part of what Write compares, so the
// very first sync after supervisor startup must apply it unconditionally;
// every later tick only needs to act on schedules whose content just
// changed, since nothing else can have altered a "never hand-edited"
// derived unit's enrollment in between (ARCHITECTURE.md §4.8).
func syncSchedules(ctx context.Context, queries *store.Queries, unitMgr *systemdunit.Manager, cliPath, tokenFile string, force bool) {
	schedules, err := queries.ListSchedules(ctx)
	if err != nil {
		log.Printf("schedule sync: listing schedules: %v", err)
		return
	}

	existingIDs, err := unitMgr.ListScheduleIDs()
	if err != nil {
		log.Printf("schedule sync: listing existing unit files: %v", err)
		return
	}
	wanted := make(map[int64]bool, len(schedules))

	anyChanged := false

	for _, sched := range schedules {
		wanted[sched.ID] = true
		stem := scheduling.UnitFileStem(sched.ID)

		onCalendar, err := scheduling.CronToOnCalendar(sched.CronExpr)
		if err != nil {
			// Schedule CRUD (task 5.7) already rejects an untranslatable
			// cron_expr before it ever reaches this table - reaching this
			// branch means something wrote around that validation. Skip
			// rather than crash the whole sync over one bad row.
			log.Printf("schedule sync: schedule %d: %v (skipping this schedule's unit)", sched.ID, err)
			continue
		}

		def := scheduling.UnitDefinition{
			ScheduleID: sched.ID,
			JobID:      sched.JobID,
			OnCalendar: onCalendar,
			Timezone:   sched.Timezone,
			Persistent: sched.CatchUpPolicy == store.CatchUpPolicyCatchUp,
			TokenFile:  tokenFile,
			CLIPath:    cliPath,
		}

		changed, err := unitMgr.Write(stem, scheduling.RenderTimerUnit(def), scheduling.RenderServiceUnit(def))
		if err != nil {
			log.Printf("schedule sync: schedule %d: writing unit: %v", sched.ID, err)
			continue
		}
		if changed {
			anyChanged = true
		}

		if changed || force {
			var enableErr error
			if sched.Enabled {
				enableErr = unitMgr.EnableNow(ctx, stem)
			} else {
				enableErr = unitMgr.DisableNow(ctx, stem)
			}
			if enableErr != nil {
				log.Printf("schedule sync: schedule %d: %v", sched.ID, enableErr)
			}
		}
	}

	for _, id := range existingIDs {
		if wanted[id] {
			continue
		}
		stem := scheduling.UnitFileStem(id)
		if err := unitMgr.DisableNow(ctx, stem); err != nil {
			log.Printf("schedule sync: schedule %d (deleted): disabling: %v", id, err)
		}
		removed, err := unitMgr.Remove(stem)
		if err != nil {
			log.Printf("schedule sync: schedule %d (deleted): removing unit: %v", id, err)
			continue
		}
		if removed {
			anyChanged = true
			log.Printf("schedule sync: removed unit for deleted schedule %d", id)
		}
	}

	if anyChanged {
		if err := unitMgr.Reload(ctx); err != nil {
			log.Printf("schedule sync: daemon-reload: %v", err)
		}
	}
}
