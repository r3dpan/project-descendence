package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/r3dpan/project-descendence/internal/client"
)

const usersUsage = `Usage: descendence user <subcommand> [flags]

Subcommands:
  list                    List users
  get <id>                Show one user
  set-role <id> -role <r> Reassign a user's role (admin only)
  revoke <id>             Revoke a user's access (admin only)

Flags for set-role:
  -role <role>            admin | operator | viewer (required)

User management (set-role/revoke) is admin-only (users:write) - everyone
else sees a 403 from the server. There is no "create" subcommand (Phase 9):
browser login is OIDC-only, and a user principal is created by its first
IdP login (JIT-provisioned with no role) rather than by an admin - set-role
is how that first role gets assigned. There is no "passwd" subcommand
either - there is no local password to change.
`

func cmdUser(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usersUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdUserList(ctx, c, args[1:])
	case "get":
		return cmdUserGet(ctx, c, args[1:])
	case "set-role":
		return cmdUserSetRole(ctx, c, args[1:])
	case "revoke":
		return cmdUserRevoke(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(usersUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown user subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, usersUsage)
		return 2
	}
}

func resolveUserID(ref string) (int64, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("user id must be numeric, got %q", ref)
	}
	return id, nil
}

func cmdUserList(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, usersUsage)
		return 2
	}

	list, err := c.ListUsers(ctx)
	if err != nil {
		printError(err)
		return 1
	}
	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render("No users."))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tROLE\tCREATED\tREVOKED")
	for _, u := range list.Items {
		revoked := "-"
		if u.RevokedAt != nil {
			revoked = *u.RevokedAt
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\n", u.ID, u.Name, u.Role, u.CreatedAt, revoked)
	}
	writer.Flush()
	return 0
}

func cmdUserGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usersUsage)
		return 2
	}
	id, err := resolveUserID(args[0])
	if err != nil {
		printError(err)
		return 2
	}
	user, err := c.GetUser(ctx, id)
	if err != nil {
		printError(err)
		return 1
	}
	fmt.Print(renderUserSummary(user))
	return 0
}

func cmdUserSetRole(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("user set-role", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usersUsage) }
	role := fs.String("role", "", "admin | operator | viewer (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		reportFlagOrder(fs.Args(), "user set-role", usersUsage)
		return 2
	}
	if *role == "" {
		printError(fmt.Errorf("-role is required"))
		return 2
	}

	id, err := resolveUserID(fs.Arg(0))
	if err != nil {
		printError(err)
		return 2
	}

	user, err := c.UpdateUser(ctx, id, client.UpdateUserParams{Role: role})
	if err != nil {
		printError(err)
		return 1
	}
	fmt.Print(renderUserSummary(user))
	return 0
}

func cmdUserRevoke(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usersUsage)
		return 2
	}
	id, err := resolveUserID(args[0])
	if err != nil {
		printError(err)
		return 2
	}
	if err := c.RevokeUser(ctx, id); err != nil {
		printError(err)
		return 1
	}
	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Revoked user %d", id)))
	return 0
}

// renderUserSummary shows one user, in the same shape as
// renderScheduleSummary/renderJobSummary so every resource reads alike.
func renderUserSummary(u client.User) string {
	status := styleValue.Render("active")
	if u.RevokedAt != nil {
		status = styleHint.Render("revoked " + *u.RevokedAt)
	}

	out := fmt.Sprintf("%s  %s\n", styleBold.Render(fmt.Sprintf("user %d: %s", u.ID, u.Name)), status)
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "role")), styleValue.Render(u.Role))
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-9s", "created")), styleValue.Render(u.CreatedAt))
	return out
}
