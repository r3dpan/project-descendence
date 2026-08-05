package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/r3dpan/project-descendence/internal/client"
)

const logsUsage = `Usage: descendence logs [flags] <run-id>

Prints a run's output.

Flags:
  -follow    Keep streaming until the run finishes
  -stderr    Print only stderr
  -stdout    Print only stdout

Output goes to this process's matching stream - the run's stdout to stdout and
its stderr to stderr - so redirection works the way it would if the script had
run locally:

  descendence logs 42 > out.txt 2> err.txt

Examples:
  descendence logs 42
  descendence logs -follow 42
`

// cmdLogs implements `descendence logs`.
func cmdLogs(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, logsUsage) }

	follow := fs.Bool("follow", false, "keep streaming until the run finishes")
	onlyStderr := fs.Bool("stderr", false, "print only stderr")
	onlyStdout := fs.Bool("stdout", false, "print only stdout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprint(os.Stderr, logsUsage)
		return 2
	}

	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		printError(fmt.Errorf("%q is not a run id", rest[0]))
		return 2
	}

	printer := logPrinter{stdout: !*onlyStderr, stderr: !*onlyStdout}

	if *follow {
		return streamLogs(ctx, c, id, 0, printer)
	}

	return printLogHistory(ctx, c, id, printer)
}

// logPrinter writes log lines to the stream they came from, so a caller can
// redirect them separately. Anything the CLI says for itself goes to stderr
// with styling; a log line is the script's own output and is printed raw -
// colouring or prefixing it would corrupt anything downstream that parses it.
type logPrinter struct {
	stdout bool
	stderr bool
}

func (p logPrinter) print(line client.LogLine) {
	switch line.Stream {
	case "stderr":
		if p.stderr {
			fmt.Fprintln(os.Stderr, line.Text)
		}
	default:
		if p.stdout {
			fmt.Println(line.Text)
		}
	}
}

// printLogHistory pages through everything a run has printed so far and
// stops, without waiting for more.
func printLogHistory(ctx context.Context, c *client.Client, id int64, printer logPrinter) int {
	var after int64
	for {
		page, err := c.GetRunLogs(ctx, id, after, 0)
		if err != nil {
			printLogsError(err, id)
			return 1
		}

		for _, line := range page.Items {
			printer.print(line)
		}

		if page.NextAfter == nil {
			return 0
		}
		after = *page.NextAfter
	}
}

// streamLogs follows a run to its end, printing output as it arrives.
//
// Returns the run's own exit code, so `descendence logs -follow` composes in a
// shell exactly as `descendence run` does. That costs one extra request at the
// end - the stream carries the final state but not the exit code, since a
// state event is deliberately small.
func streamLogs(ctx context.Context, c *client.Client, id int64, after int64, printer logPrinter) int {
	err := c.FollowRunLogs(ctx, id, after, client.LogStream{
		OnLine: printer.print,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "%s\n", styleHint.Render(
				fmt.Sprintf("stopped watching run %d; it is still going (descendence cancel %d stops it)", id, id)))
			return 130
		}
		printLogsError(err, id)
		return 1
	}

	final, err := c.GetRun(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}

	return exitCodeFor(final)
}

// printLogsError explains the one status whose meaning is not obvious from
// its text: a 410 means the run is fine and its output is not.
func printLogsError(err error, id int64) {
	printError(err)

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusGone {
		fmt.Fprintln(os.Stderr, styleHint.Render(
			fmt.Sprintf("  run %d still exists; only its output has passed the retention window", id)))
	}
}

const cancelUsage = `Usage: descendence cancel <run-id>

Requests cancellation of a run.

A queued run is cancelled immediately - it has no container yet. A running run
is stopped by the supervisor, which usually takes about a second; this command
waits for that to happen rather than reporting a cancellation that has not
occurred yet.

Examples:
  descendence cancel 42
`

// cmdCancel implements `descendence cancel`.
//
// It waits for the run to actually reach a terminal state rather than printing
// "cancelled" the moment the server accepts the request. The API returns 202
// precisely because a running run is still running at that point (task 2.8),
// and a CLI that reported success there would be describing the request, not
// the outcome - which is the difference that matters when the next line of
// somebody's script assumes the container is gone.
func cmdCancel(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, cancelUsage)
		return 2
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		printError(fmt.Errorf("%q is not a run id", args[0]))
		return 2
	}

	run, err := c.CancelRun(ctx, id)
	if err != nil {
		printError(err)

		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			fmt.Fprintln(os.Stderr, styleHint.Render("  a run that has finished cannot be cancelled"))
		}
		return 1
	}

	if run.IsTerminal() {
		fmt.Printf("run %d: %s\n", run.ID, run.State)
		return 0
	}

	fmt.Fprintf(os.Stderr, "%s\n", styleHint.Render(
		fmt.Sprintf("run %d: cancellation requested, waiting for the supervisor to stop it", run.ID)))

	final, err := c.PollRun(ctx, id, pollInterval, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "%s\n", styleHint.Render(
				fmt.Sprintf("stopped waiting; run %d is still being cancelled", id)))
			return 130
		}
		printError(err)
		return 1
	}

	fmt.Printf("run %d: %s\n", final.ID, final.State)

	return 0
}
