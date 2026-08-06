package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/r3dpan/project-descendence/internal/client"
)

const tokensUsage = `Usage: descendence token <subcommand> [flags]

Subcommands:
  list                    List tokens
  get <id>                Show one token
  create -name <name>     Create a token (admin only)
  revoke <id>             Revoke a token (admin only)

Flags for create:
  -name <name>            Required.
  -role <role>            admin | operator | viewer (required)
  -expires <duration>     Optional, e.g. "720h" for 30 days

Token management is admin-only (users:write) - a non-admin sees a 403 from
the server. The plaintext token is shown exactly once, on create.
`

func cmdToken(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, tokensUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdTokenList(ctx, c, args[1:])
	case "get":
		return cmdTokenGet(ctx, c, args[1:])
	case "create":
		return cmdTokenCreate(ctx, c, args[1:])
	case "revoke":
		return cmdTokenRevoke(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(tokensUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown token subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, tokensUsage)
		return 2
	}
}

func cmdTokenList(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, tokensUsage)
		return 2
	}

	list, err := c.ListTokens(ctx)
	if err != nil {
		printError(err)
		return 1
	}
	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render("No tokens."))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tROLE\tHINT\tCREATED\tEXPIRES\tREVOKED")
	for _, t := range list.Items {
		expires, revoked := "-", "-"
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.Format(time.RFC3339)
		}
		if t.RevokedAt != nil {
			revoked = *t.RevokedAt
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Role, t.TokenHint, t.CreatedAt, expires, revoked)
	}
	writer.Flush()
	return 0
}

func cmdTokenGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, tokensUsage)
		return 2
	}
	id, err := resolveUserID(args[0])
	if err != nil {
		printError(err)
		return 2
	}
	tok, err := c.GetToken(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}
	fmt.Print(renderTokenSummary(tok))
	return 0
}

func cmdTokenCreate(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, tokensUsage) }
	name := fs.String("name", "", "token name (required)")
	role := fs.String("role", "", "admin | operator | viewer (required)")
	expires := fs.String("expires", "", `optional duration, e.g. "720h"`)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *role == "" {
		printError(fmt.Errorf("-name and -role are required"))
		return 2
	}

	params := client.CreateTokenParams{Name: *name, Role: *role}
	if *expires != "" {
		d, err := time.ParseDuration(*expires)
		if err != nil {
			printError(fmt.Errorf("-expires must be a Go duration like \"720h\": %w", err))
			return 2
		}
		t := time.Now().Add(d)
		params.ExpiresAt = &t
	}

	result, err := c.CreateToken(ctx, params)
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderTokenSummary(result.Token))
	fmt.Printf("\n%s\n\n  %s\n\n", styleBold.Render("Token (shown once - store it now):"), result.PlaintextToken)
	return 0
}

func cmdTokenRevoke(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, tokensUsage)
		return 2
	}
	id, err := resolveUserID(args[0])
	if err != nil {
		printError(err)
		return 2
	}
	if err := c.RevokeToken(ctx, id); err != nil {
		printError(err)
		return 1
	}
	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Revoked token %d", id)))
	return 0
}

// renderTokenSummary shows one token, matching renderUserSummary's shape.
func renderTokenSummary(t client.Token) string {
	status := styleValue.Render("active")
	if t.RevokedAt != nil {
		status = styleHint.Render("revoked " + *t.RevokedAt)
	}

	out := fmt.Sprintf("%s  %s\n", styleBold.Render(fmt.Sprintf("token %d: %s", t.ID, t.Name)), status)
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "role")), styleValue.Render(t.Role))
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "hint")), styleValue.Render(t.TokenHint))
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "created")), styleValue.Render(t.CreatedAt))
	if t.ExpiresAt != nil {
		out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "expires")), styleValue.Render(t.ExpiresAt.Format(time.RFC3339)))
	}
	return out
}
