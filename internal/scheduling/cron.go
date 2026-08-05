// Package scheduling translates a schedule's cron expression into a
// systemd OnCalendar= string and renders the .timer/.service unit pair the
// supervisor writes for it, per ARCHITECTURE.md §4.8 (decision #27, task
// 5.3). It does not talk to systemctl - that is internal/systemdunit, given
// this package's output.
package scheduling

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
)

// weekdayAbbrev maps cron's day-of-week numbering (0-7, both 0 and 7 mean
// Sunday) onto systemd's weekday abbreviations.
var weekdayAbbrev = map[int]string{
	0: "Sun", 1: "Mon", 2: "Tue", 3: "Wed", 4: "Thu", 5: "Fri", 6: "Sat", 7: "Sun",
}

type cronField struct {
	name     string
	min, max int
}

var (
	fieldMinute = cronField{"minute", 0, 59}
	fieldHour   = cronField{"hour", 0, 23}
	fieldDom    = cronField{"day of month", 1, 31}
	fieldMonth  = cronField{"month", 1, 12}
	fieldDow    = cronField{"day of week", 0, 7}
)

// CronToOnCalendar translates a standard 5-field cron expression (minute
// hour dom month dow) into a systemd OnCalendar= string.
//
// Deliberately scoped to a conservative subset that maps cleanly onto
// systemd's calendar syntax: a single value, "*", a simple "*/N" step, or a
// comma-list of single values, per field. Anything else - range syntax
// (1-5), and day-of-month combined with day-of-week both restricted - is
// rejected by name rather than risk translating it subtly wrong and firing
// at the wrong time silently. This matches this codebase's existing
// "unknown key is an error, not silently wrong" posture
// (internal/manifest.Parse) applied to cron syntax instead of a manifest.
func CronToOnCalendar(cronExpr string) (string, error) {
	// robfig/cron/v3 validates the expression is legal standard cron before
	// this package's narrower translator has to reason about it - catches
	// anything genuinely malformed with a well-tested parser rather than
	// this hand-rolled one.
	if _, err := cron.ParseStandard(cronExpr); err != nil {
		return "", fmt.Errorf("scheduling: invalid cron expression %q: %w", cronExpr, err)
	}

	parts := strings.Fields(cronExpr)
	if len(parts) != 5 {
		return "", fmt.Errorf("scheduling: expected 5 fields (minute hour dom month dow), got %d in %q", len(parts), cronExpr)
	}

	minute, err := translateField(fieldMinute, parts[0])
	if err != nil {
		return "", err
	}
	hour, err := translateField(fieldHour, parts[1])
	if err != nil {
		return "", err
	}
	dom, err := translateField(fieldDom, parts[2])
	if err != nil {
		return "", err
	}
	month, err := translateField(fieldMonth, parts[3])
	if err != nil {
		return "", err
	}
	dow, err := translateField(fieldDow, parts[4])
	if err != nil {
		return "", err
	}

	// Cron's day-of-month/day-of-week combination is an OR when both are
	// restricted ("run on the 1st OR on a Monday") - OnCalendar= has no
	// direct equivalent for that, so rather than silently translate it as
	// an AND (a materially different schedule), reject it by name.
	if parts[2] != "*" && parts[4] != "*" {
		return "", fmt.Errorf("scheduling: combined day-of-month and day-of-week restrictions are not supported (cron's OR semantics here has no direct OnCalendar= equivalent) - restrict only one of the two in %q", cronExpr)
	}

	date := fmt.Sprintf("*-%s-%s", month, dom)
	timeOfDay := fmt.Sprintf("%s:%s:00", hour, minute)

	if dow != "*" {
		return fmt.Sprintf("%s %s %s", dow, date, timeOfDay), nil
	}
	return fmt.Sprintf("%s %s", date, timeOfDay), nil
}

// translateField converts one cron field into the equivalent OnCalendar=
// component, per the scope documented on CronToOnCalendar.
func translateField(f cronField, raw string) (string, error) {
	isWeekday := f == fieldDow

	if strings.Contains(raw, "-") {
		return "", fmt.Errorf("scheduling: %s: range syntax (e.g. 1-5) is not supported, list the values explicitly instead (got %q)", f.name, raw)
	}

	if raw == "*" {
		return "*", nil
	}

	if step, ok := strings.CutPrefix(raw, "*/"); ok {
		if isWeekday {
			return "", fmt.Errorf("scheduling: %s: step syntax (*/N) is not supported for day-of-week (got %q)", f.name, raw)
		}
		n, err := strconv.Atoi(step)
		if err != nil || n <= 0 {
			return "", fmt.Errorf("scheduling: %s: invalid step %q", f.name, raw)
		}
		return fmt.Sprintf("%d/%d", f.min, n), nil
	}

	values := strings.Split(raw, ",")
	out := make([]string, 0, len(values))
	for _, v := range values {
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("scheduling: %s: unsupported syntax %q - only single values, \"*\", \"*/N\" steps and comma-lists are supported", f.name, v)
		}
		if n < f.min || n > f.max {
			return "", fmt.Errorf("scheduling: %s: value %d out of range [%d,%d]", f.name, n, f.min, f.max)
		}
		if isWeekday {
			out = append(out, weekdayAbbrev[n])
		} else {
			out = append(out, strconv.Itoa(n))
		}
	}
	return strings.Join(out, ","), nil
}
