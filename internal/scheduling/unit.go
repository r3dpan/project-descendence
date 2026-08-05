package scheduling

import "fmt"

// defaultCLIPath is used when a UnitDefinition doesn't name one explicitly -
// relies on the descendence binary being on the invoking systemd user
// service's PATH, which is not guaranteed to match an interactive shell's.
// Callers that know the CLI's install location should set CLIPath instead.
const defaultCLIPath = "descendence"

// UnitDefinition is the rendering input for one schedule's unit pair -
// exactly the fields a rendered .timer/.service pair depends on, so two
// schedules with identical fields render identical units.
type UnitDefinition struct {
	ScheduleID int64
	JobID      int64
	OnCalendar string // from CronToOnCalendar
	Timezone   string
	Persistent bool   // catch_up_policy == "catch_up" (task 5.4)
	TokenFile  string // EnvironmentFile the ExecStart's CLI invocation reads (task 5.3)
	CLIPath    string // absolute path to the descendence binary; empty falls back to defaultCLIPath
}

// UnitFileStem is the filename (without extension) both the .timer and
// .service files for a schedule share, and the systemctl unit name systemd
// operations (internal/systemdunit) address it by.
func UnitFileStem(scheduleID int64) string {
	return fmt.Sprintf("descendence-schedule-%d", scheduleID)
}

// RenderServiceUnit renders the .service half of the pair: a oneshot that
// runs the CLI's trigger subcommand, authenticated via EnvironmentFile
// rather than an inline token (task 5.3 - a generated unit file under
// ~/.config/systemd/user/ is not private the way a permission-restricted
// env file is).
func RenderServiceUnit(def UnitDefinition) string {
	cliPath := def.CLIPath
	if cliPath == "" {
		cliPath = defaultCLIPath
	}
	return fmt.Sprintf(`[Unit]
Description=Descendence schedule %d (job %d)

[Service]
Type=oneshot
EnvironmentFile=%s
ExecStart=%s schedule trigger %d
`, def.ScheduleID, def.JobID, def.TokenFile, cliPath, def.ScheduleID)
}

// RenderTimerUnit renders the .timer half: OnCalendar= drives firing,
// Persistent= is the catch-up policy (task 5.4 - fires once to catch up,
// not once per missed window, per decision #27), TimeZone= is the
// schedule's own timezone rather than embedding it into OnCalendar= (task
// 5.5).
func RenderTimerUnit(def UnitDefinition) string {
	persistent := "false"
	if def.Persistent {
		persistent = "true"
	}
	return fmt.Sprintf(`[Unit]
Description=Descendence schedule %d timer (job %d)

[Timer]
OnCalendar=%s
Persistent=%s
TimeZone=%s

[Install]
WantedBy=timers.target
`, def.ScheduleID, def.JobID, def.OnCalendar, persistent, def.Timezone)
}
