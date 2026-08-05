package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/r3dpan/project-descendence/internal/client"
)

const schedulesUsage = `Usage: descendence schedule <subcommand> [flags]

Subcommands:
  list <job name|id>       List a job's schedules
  get <id>                 Show one schedule
  create <job name|id>     Create a schedule for a job
  update <id>              Change a schedule
  delete <id>              Delete a schedule
  trigger <id>             Fire a schedule now, the way its timer does

Flags for create/update:
  -cron <expr>            Standard 5-field cron syntax (required for create)
  -timezone <tz>          IANA timezone name (default "UTC")
  -catch-up <policy>      skip | catch_up (default "skip")
  -overlap <policy>       skip | queue | concurrent (default "skip")
  -enabled <bool>         true | false (default "true" for create)

A schedule fires a job through a generated systemd (user) timer, not this
command - "trigger" calls the same endpoint the timer does, so it is the
way to test a schedule without waiting for it. cronExpr supports a single
value, "*", a simple "*/N" step, or a comma-list per field; range syntax
(1-5) and combined day-of-month + day-of-week restrictions are rejected.
`

func cmdSchedule(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdScheduleList(ctx, c, args[1:])
	case "get":
		return cmdScheduleGet(ctx, c, args[1:])
	case "create":
		return cmdScheduleCreate(ctx, c, args[1:])
	case "update":
		return cmdScheduleUpdate(ctx, c, args[1:])
	case "delete":
		return cmdScheduleDelete(ctx, c, args[1:])
	case "trigger":
		return cmdScheduleTrigger(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(schedulesUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown schedule subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}
}

// resolveScheduleID parses a schedule reference. Unlike jobs/runtimes, a
// schedule has no name to resolve by - only a numeric id.
func resolveScheduleID(ref string) (int64, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("schedule id must be numeric, got %q", ref)
	}
	return id, nil
}

func cmdScheduleList(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}

	job, err := resolveJob(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	list, err := c.ListSchedulesByJob(ctx, job.ID)
	if err != nil {
		printError(err)
		return 1
	}

	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("No schedules for %s. Create one with: descendence schedule create %s -cron '<expr>'", job.Name, job.Name)))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tCRON\tTIMEZONE\tCATCH-UP\tOVERLAP\tENABLED\tNEXT DUE")
	for _, sched := range list.Items {
		nextDue := "-"
		if sched.NextDueAt != nil {
			nextDue = *sched.NextDueAt
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%t\t%s\n",
			sched.ID, sched.CronExpr, sched.Timezone, sched.CatchUpPolicy, sched.OverlapPolicy, sched.Enabled, nextDue)
	}
	writer.Flush()
	return 0
}

func cmdScheduleGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}

	id, err := resolveScheduleID(args[0])
	if err != nil {
		printError(err)
		return 2
	}

	sched, err := c.GetSchedule(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderScheduleSummary(sched))
	return 0
}

func cmdScheduleCreate(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("schedule create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, schedulesUsage) }
	cronExpr := fs.String("cron", "", "standard 5-field cron expression (required)")
	timezone := fs.String("timezone", "", "IANA timezone name (default \"UTC\")")
	catchUp := fs.String("catch-up", "", "skip | catch_up (default \"skip\")")
	overlap := fs.String("overlap", "", "skip | queue | concurrent (default \"skip\")")
	enabledFlag := fs.String("enabled", "", "true | false (default \"true\")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		reportFlagOrder(fs.Args(), "schedule create", schedulesUsage)
		return 2
	}
	if *cronExpr == "" {
		printError(fmt.Errorf("-cron is required"))
		return 2
	}

	job, err := resolveJob(ctx, c, fs.Arg(0))
	if err != nil {
		printError(err)
		return 1
	}

	params := client.CreateScheduleParams{
		CronExpr:      *cronExpr,
		Timezone:      *timezone,
		CatchUpPolicy: *catchUp,
		OverlapPolicy: *overlap,
	}
	if *enabledFlag != "" {
		enabled, err := strconv.ParseBool(*enabledFlag)
		if err != nil {
			printError(fmt.Errorf("-enabled must be true or false, got %q", *enabledFlag))
			return 2
		}
		params.Enabled = &enabled
	}

	sched, err := c.CreateSchedule(ctx, job.ID, params)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderScheduleSummary(sched))
	return 0
}

func cmdScheduleUpdate(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("schedule update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, schedulesUsage) }
	cronExpr := fs.String("cron", "", "standard 5-field cron expression")
	timezone := fs.String("timezone", "", "IANA timezone name")
	catchUp := fs.String("catch-up", "", "skip | catch_up")
	overlap := fs.String("overlap", "", "skip | queue | concurrent")
	enabledFlag := fs.String("enabled", "", "true | false")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		reportFlagOrder(fs.Args(), "schedule update", schedulesUsage)
		return 2
	}

	id, err := resolveScheduleID(fs.Arg(0))
	if err != nil {
		printError(err)
		return 2
	}

	var params client.UpdateScheduleParams
	if *cronExpr != "" {
		params.CronExpr = cronExpr
	}
	if *timezone != "" {
		params.Timezone = timezone
	}
	if *catchUp != "" {
		params.CatchUpPolicy = catchUp
	}
	if *overlap != "" {
		params.OverlapPolicy = overlap
	}
	if *enabledFlag != "" {
		enabled, err := strconv.ParseBool(*enabledFlag)
		if err != nil {
			printError(fmt.Errorf("-enabled must be true or false, got %q", *enabledFlag))
			return 2
		}
		params.Enabled = &enabled
	}

	sched, err := c.UpdateSchedule(ctx, id, params)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderScheduleSummary(sched))
	return 0
}

func cmdScheduleDelete(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}

	id, err := resolveScheduleID(args[0])
	if err != nil {
		printError(err)
		return 2
	}

	if err := c.DeleteSchedule(ctx, id); err != nil {
		printError(err)
		return 1
	}

	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Deleted schedule %d", id)))
	return 0
}

func cmdScheduleTrigger(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, schedulesUsage)
		return 2
	}

	id, err := resolveScheduleID(args[0])
	if err != nil {
		printError(err)
		return 2
	}

	result, err := c.TriggerSchedule(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}

	if result.Skipped {
		fmt.Fprintln(os.Stderr, styleHint.Render("skipped: "+result.Reason))
		return 0
	}

	fmt.Print(renderRunSummary(*result.Run, false))
	return 0
}

// renderScheduleSummary shows one schedule, in the same shape as
// renderJobSummary/renderRuntimeSummary so all three read alike.
func renderScheduleSummary(sched client.Schedule) string {
	var b strings.Builder

	b.WriteString(styleBold.Render(fmt.Sprintf("schedule %d", sched.ID)))
	b.WriteString("  ")
	if sched.Enabled {
		b.WriteString(styleValue.Render("enabled"))
	} else {
		b.WriteString(styleHint.Render("disabled"))
	}
	b.WriteString("\n")

	field := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString("  ")
		b.WriteString(styleLabel.Render(fmt.Sprintf("%-10s", label)))
		b.WriteString(styleValue.Render(value))
		b.WriteString("\n")
	}

	field("job", strconv.FormatInt(sched.JobID, 10))
	field("cron", sched.CronExpr)
	field("timezone", sched.Timezone)
	field("catch-up", sched.CatchUpPolicy)
	field("overlap", sched.OverlapPolicy)
	if sched.NextDueAt != nil {
		field("next due", *sched.NextDueAt)
	}

	return b.String()
}
