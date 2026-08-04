package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/r3dpan/project-descendence/internal/client"
)

const runsUsage = `Usage: descendence runs <subcommand> [flags]

Subcommands:
  list          List runs, newest first
  get <id>      Show one run

Flags for list:
  -limit <n>    Runs per page (1-200, server default if omitted)
  -all          Follow every page instead of stopping after the first

On a terminal, list opens a browsable table: up/down to move, enter to show
a run in full, q to quit. It loads further pages as you scroll. When piped,
it prints aligned rows and exits.
`

func cmdRuns(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, runsUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdRunsList(ctx, c, args[1:])
	case "get":
		return cmdRunsGet(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(runsUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, runsUsage)
		return 2
	}
}

// cmdRunsGet renders one run with the same block `descendence run` prints
// when a run finishes - a run should look identical however you arrived at
// it.
func cmdRunsGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "Usage: descendence runs get <id>\n")
		return 2
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		printError(fmt.Errorf("%q is not a run id", args[0]))
		return 2
	}

	run, err := c.GetRun(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderRunSummary(run, false))

	return 0
}

func cmdRunsList(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, runsUsage) }

	limit := fs.Int("limit", 0, "runs per page")
	all := fs.Bool("all", false, "follow every page")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Fetch the first page before deciding how to render it, so a bad
	// token or an out-of-range limit is a plain error message rather than
	// something the user has to read out of a half-drawn TUI.
	page, err := c.ListRuns(ctx, client.ListRunsParams{Limit: int32(*limit)})
	if err != nil {
		printError(err)
		return 1
	}

	if len(page.Items) == 0 {
		fmt.Println(styleHint.Render("no runs yet"))
		return 0
	}

	if *all || !isTTY(os.Stdout) {
		return listPlain(ctx, c, page, int32(*limit), *all)
	}

	return listInteractive(ctx, c, page, int32(*limit))
}

// listPlain prints aligned rows and exits - the pipeable path. With -all it
// follows nextCursor to the end; otherwise it prints one page and says how
// to get the rest, rather than silently looking like the whole list.
func listPlain(ctx context.Context, c *client.Client, page client.RunList, limit int32, all bool) int {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tEXIT\tQUEUED\tDURATION\tIMAGE\tARGV")

	for {
		for _, run := range page.Items {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
				run.ID,
				run.State,
				exitCodeText(run),
				formatRelative(run.QueuedAt),
				runDuration(run),
				run.ImageRef,
				strings.Join(run.Argv, " "))
		}

		if !all || page.NextCursor == nil {
			break
		}

		next, err := c.ListRuns(ctx, client.ListRunsParams{Limit: limit, Cursor: *page.NextCursor})
		if err != nil {
			w.Flush()
			printError(err)
			return 1
		}
		page = next
	}

	w.Flush()

	if !all && page.NextCursor != nil {
		fmt.Fprintln(os.Stderr, styleHint.Render("more runs available; pass -all for the rest"))
	}

	return 0
}

// exitCodeText renders a run's exit code for a table cell. A run that never
// produced one (still going, timed out, lost) gets a dash rather than a
// misleading 0.
func exitCodeText(run client.Run) string {
	if run.ExitCode == nil {
		return "-"
	}
	return strconv.Itoa(int(*run.ExitCode))
}

// runDuration is how long a run actually executed, or how long it has been
// executing so far. Queued runs have no duration yet.
func runDuration(run client.Run) string {
	switch {
	case run.StartedAt == nil:
		return "-"
	case run.FinishedAt == nil:
		return formatDuration(time.Since(*run.StartedAt))
	default:
		return formatDuration(run.FinishedAt.Sub(*run.StartedAt))
	}
}

// formatRelative renders a timestamp as an age, which is what actually
// matters when scanning a list of runs - "4m ago" answers the question an
// absolute timestamp makes you compute.
func formatRelative(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
