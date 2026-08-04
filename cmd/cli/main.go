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
  whoami  Show which principal the configured token resolves to
  help    Show this message

Configuration:
  DESCENDENCE_URL     Base URL of the API server, e.g. http://127.0.0.1:8080
  DESCENDENCE_TOKEN   Bearer token

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

	// Ctrl-C stops the CLI, not the run - there is no cancel endpoint until
	// Phase 2, and pretending otherwise would be a lie about what happened
	// to the container.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := client.New(cfg.baseURL, cfg.token)

	switch command {
	case "run":
		return cmdRun(ctx, c, rest)
	case "whoami":
		return cmdWhoAmI(ctx, c)
	default:
		printError(fmt.Errorf("unknown command %q", command))
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
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
