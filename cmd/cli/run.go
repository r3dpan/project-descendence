package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/r3dpan/project-descendence/internal/client"
)

const runUsage = `Usage: descendence run [flags] <image> [--] <argv...>

Creates a run and watches it until it finishes.

Flags:
  -follow              Stream the run's output as it happens, instead of
                       showing its state
  -timeout <seconds>   Maximum run duration (server decides if omitted)
  -key <string>        Idempotency-Key: retrying with the same key returns
                       the original run instead of queueing a second one
  -detach              Print the run id and exit without watching

Arguments after the image are passed to the container as argv, exactly as
written - they are never interpreted by a shell. Use -- before them if any
would otherwise look like a flag to this command.

Examples:
  descendence run docker.io/library/alpine:latest echo hello
  descendence run -timeout 30 docker.io/library/alpine:latest sleep 5
  descendence run docker.io/library/alpine:latest -- ls -la /
  descendence run -follow docker.io/library/alpine:latest sh -c 'echo hi; sleep 5'
`

// cmdRun implements `descendence run`. It exits with the run's own exit
// code when there is one, so the CLI composes with shell scripts the same
// way running the command locally would.
func cmdRun(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, runUsage) }

	timeout := fs.Int("timeout", 0, "maximum run duration in seconds")
	key := fs.String("key", "", "Idempotency-Key for this request")
	detach := fs.Bool("detach", false, "create the run and exit without watching")
	follow := fs.Bool("follow", false, "stream the run's output as it happens")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, runUsage)
		return 2
	}

	image := rest[0]
	argv := rest[1:]
	// An explicit -- separator is allowed for readability; flag parsing has
	// already stopped by this point, so it arrives as a plain argument and
	// must not be passed to the container.
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printError(errors.New("a command to run is required after the image"))
		return 2
	}

	run, err := c.CreateRun(ctx, client.CreateRunParams{
		ImageRef:       image,
		Argv:           argv,
		TimeoutSeconds: int32(*timeout),
		IdempotencyKey: *key,
	})
	if err != nil {
		printError(err)
		return 1
	}

	if *detach {
		fmt.Println(run.ID)
		return 0
	}

	// Following prints the run's own output; watching prints its state. They
	// are alternatives rather than layers, because a spinner and a script's
	// stdout cannot share a terminal without one corrupting the other.
	//
	// after=0: this run was created a moment ago, so there is no history to
	// skip, and starting at 0 means output printed between the create and the
	// stream still arrives (task 2.1 - libpod replays from the beginning, and
	// the endpoint serves history before it follows).
	if *follow {
		return streamLogs(ctx, c, run.ID, 0, logPrinter{stdout: true, stderr: true})
	}

	final, err := watchRun(ctx, c, run)
	if err != nil {
		// Ctrl-C on the non-TTY path arrives as a cancelled context (the
		// TUI path gets it as a keypress instead, since raw mode swallows
		// the signal). Either way the run keeps going - say so plainly
		// rather than printing "context canceled" at the user.
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "%s\n", styleHint.Render(
				fmt.Sprintf("stopped watching run %d; it is still going", run.ID)))
			return 130
		}
		printError(err)
		return 1
	}

	return exitCodeFor(final)
}

// watchRun follows a run to a terminal state, using the bubbletea view when
// stdout is a terminal and plain lines when it isn't - a CLI being piped
// into another program must not emit spinners or cursor movement.
func watchRun(ctx context.Context, c *client.Client, run client.Run) (client.Run, error) {
	if !isTTY(os.Stdout) {
		return watchPlain(ctx, c, run)
	}

	model, err := tea.NewProgram(newWatchModel(ctx, c, run)).Run()
	if err != nil {
		return client.Run{}, err
	}

	final := model.(watchModel)
	if final.err != nil {
		return client.Run{}, final.err
	}

	return final.run, nil
}

// watchPlain is the non-interactive path: one line per state change (not
// per poll), then the same summary block the TUI renders. Being
// line-oriented and append-only makes it safe to pipe, tee or log.
func watchPlain(ctx context.Context, c *client.Client, run client.Run) (client.Run, error) {
	lastState := ""

	final, err := c.PollRun(ctx, run.ID, pollInterval, func(observed client.Run) {
		if observed.State == lastState {
			return
		}
		lastState = observed.State
		fmt.Printf("run %d: %s\n", observed.ID, observed.State)
	})
	if err != nil {
		return client.Run{}, err
	}

	fmt.Print(renderRunSummary(final, false))

	return final, nil
}

// exitCodeFor maps a finished run onto this process's exit code: the
// container's own code when there is one, so `descendence run ... ; echo $?`
// behaves like running the command locally. A run that failed without ever
// producing an exit code (timeout, lost, cancelled, an infrastructure
// failure) exits 1.
func exitCodeFor(run client.Run) int {
	if run.ExitCode != nil {
		return int(*run.ExitCode)
	}
	if run.State == client.StateSucceeded {
		return 0
	}
	return 1
}
