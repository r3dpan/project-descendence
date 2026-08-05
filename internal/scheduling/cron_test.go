package scheduling

import (
	"os/exec"
	"testing"
)

func TestCronToOnCalendarSupported(t *testing.T) {
	cases := []struct {
		name string
		cron string
		want string
	}{
		{"every 5 minutes", "*/5 * * * *", "*-*-* *:0/5:00"},
		{"hourly", "0 * * * *", "*-*-* *:0:00"},
		{"daily at 2am", "0 2 * * *", "*-*-* 2:0:00"},
		{"every monday 9am", "0 9 * * 1", "Mon *-*-* 9:0:00"},
		{"comma list of minutes", "0,15,30,45 * * * *", "*-*-* *:0,15,30,45:00"},
		{"every other day", "0 3 */2 * *", "*-*-1/2 3:0:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CronToOnCalendar(tc.cron)
			if err != nil {
				t.Fatalf("CronToOnCalendar(%q) returned error: %v", tc.cron, err)
			}
			if got != tc.want {
				t.Fatalf("CronToOnCalendar(%q) = %q, want %q", tc.cron, got, tc.want)
			}
		})
	}
}

func TestCronToOnCalendarRejected(t *testing.T) {
	cases := []struct {
		name string
		cron string
	}{
		{"range syntax", "0 9-17 * * *"},
		{"combined dom and dow", "0 9 1 * 1"},
		{"step on day-of-week", "0 9 * * */2"},
		{"malformed", "not a cron expression"},
		{"wrong field count", "* * * *"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CronToOnCalendar(tc.cron); err == nil {
				t.Fatalf("CronToOnCalendar(%q) succeeded, want an error naming the unsupported syntax", tc.cron)
			}
		})
	}
}

// TestCronToOnCalendarAgreesWithSystemd cross-checks every accepted
// translation against systemd-analyze calendar, which parses OnCalendar=
// expressions the same way a real .timer unit would. This package's
// translator is hand-rolled and systemd's calendar grammar has real edge
// cases (§4.8/decision #27 flags this explicitly) - skips cleanly if the
// binary isn't available, same pattern the DB/Podman integration tests use
// for their own dependencies.
func TestCronToOnCalendarAgreesWithSystemd(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze not available")
	}

	exprs := []string{
		"*/5 * * * *",
		"0 * * * *",
		"0 2 * * *",
		"0 9 * * 1",
		"0,15,30,45 * * * *",
		"0 3 */2 * *",
	}
	for _, cronExpr := range exprs {
		t.Run(cronExpr, func(t *testing.T) {
			onCalendar, err := CronToOnCalendar(cronExpr)
			if err != nil {
				t.Fatalf("CronToOnCalendar(%q): %v", cronExpr, err)
			}
			out, err := exec.Command("systemd-analyze", "calendar", onCalendar).CombinedOutput()
			if err != nil {
				t.Fatalf("systemd-analyze calendar %q rejected the translation of %q: %v\n%s", onCalendar, cronExpr, err, out)
			}
		})
	}
}
