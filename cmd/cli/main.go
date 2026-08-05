// Command descendence is the CLI client for the Descendence API. It is a
// plain HTTP client like any other (ARCHITECTURE.md §2 principle 3) - it
// never touches Postgres or Podman directly.
//
// Rendering is bubbletea/lipgloss (ARCHITECTURE.md §6 decision #17);
// command dispatch and flag parsing are deliberately stdlib.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/r3dpan/project-descendence/internal/client"
)

const usage = `descendence - run scripts in containers, through the Descendence API

Usage:
  descendence <command> [flags] [arguments]

Commands:
  run     Create a run and watch it until it finishes
  logs    Print a run's output, optionally following it live
  cancel  Stop a run
  runs    List runs, or show one in full
  whoami  Show which principal the configured token resolves to
  config  Show where the URL and token are being read from
  help    Show this message

Configuration, in order of precedence:

  1. Environment
       DESCENDENCE_URL     Base URL of the API server
       DESCENDENCE_TOKEN   Bearer token
  2. ~/.config/descendence/config (or $DESCENDENCE_CONFIG)

         # comments and blank lines are ignored
         url   = http://127.0.0.1:8080
         token = sra_live_...

     It holds a token, so it should be mode 600.

Run "descendence <command> -h" for a command's own flags.
`

func main() {
	os.Exit(run())
}

// run holds the real main so deferred cleanup still happens - os.Exit in
// main() would skip it.
func run() int {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	command, rest := args[0], args[1:]

	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}

	cfg, err := loadConfig()
	if err != nil {
		printError(err)
		return 2
	}

	// Ctrl-C stops the CLI, not the run. That is a deliberate choice rather
	// than a limitation now that `descendence cancel` exists (task 2.8):
	// detaching from something is not the same as ending it, and a watch
	// command that killed a job on Ctrl-C would make interrupting a `logs`
	// command dangerous. The commands that stop watching say how to stop the
	// run instead.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := client.New(cfg.baseURL, cfg.token)

	switch command {
	case "run":
		return cmdRun(ctx, c, rest)
	case "logs":
		return cmdLogs(ctx, c, rest)
	case "cancel":
		return cmdCancel(ctx, c, rest)
	case "runs":
		return cmdRuns(ctx, c, rest)
	case "whoami":
		return cmdWhoAmI(ctx, c)
	case "config":
		return cmdConfig(cfg)
	default:
		printError(fmt.Errorf("unknown command %q", command))
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
}

// cmdConfig shows the resolved settings and where each came from - the
// answer to "why is it talking to the wrong server". The token is never
// printed in full, only the same trailing hint the server stores, which is
// enough to tell two tokens apart.
func cmdConfig(cfg config) int {
	field := func(label, value string, from source) {
		fmt.Printf("  %s%s %s\n",
			styleLabel.Render(fmt.Sprintf("%-7s", label)),
			styleValue.Render(value),
			styleHint.Render("("+string(from)+")"))
	}

	fmt.Println(styleBold.Render("resolved configuration"))
	field("url", cfg.baseURL, cfg.urlSource)
	field("token", tokenHint(cfg.token), cfg.tokenSource)

	fmt.Printf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-7s", "file")), styleValue.Render(cfg.path))
	if _, err := os.Stat(cfg.path); err != nil {
		fmt.Println(styleHint.Render("          (does not exist)"))
	}

	return 0
}

func cmdWhoAmI(ctx context.Context, c *client.Client) int {
	principal, err := c.WhoAmI(ctx)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Printf("%s %s\n",
		styleBold.Render(principal.Name),
		styleLabel.Render(fmt.Sprintf("(#%d, %s)", principal.ID, principal.Kind)))
	fmt.Printf("  %s%s\n",
		styleLabel.Render(fmt.Sprintf("%-9s", "scopes")),
		styleValue.Render(fmt.Sprint(principal.Scopes)))

	return 0
}

// printError writes one styled line to stderr, plus a hint for the one
// error whose cause is almost always configuration rather than anything
// wrong with this particular invocation.
func printError(err error) {
	fmt.Fprintf(os.Stderr, "%s%v\n", styleError.Render("error: "), err)

	if errors.Is(err, client.ErrUnauthorized) {
		fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("  check %s", envToken)))
	}
}
