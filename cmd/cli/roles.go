package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/r3dpan/project-descendence/internal/client"
)

const rolesUsage = `Usage: descendence role <subcommand> [flags]

Subcommands:
  list          List the built-in roles and their permissions
  get <name>    Show one role

Roles are fixed (admin, operator, viewer) - there is no create/edit/delete,
per ARCHITECTURE.md §6 decision #30.
`

func cmdRole(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, rolesUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdRoleList(ctx, c, args[1:])
	case "get":
		return cmdRoleGet(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(rolesUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown role subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, rolesUsage)
		return 2
	}
}

func cmdRoleList(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, rolesUsage)
		return 2
	}

	list, err := c.ListRoles(ctx)
	if err != nil {
		printError(err)
		return 1
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tDESCRIPTION\tPERMISSIONS")
	for _, r := range list.Items {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", r.Name, r.Description, strings.Join(r.Permissions, ", "))
	}
	writer.Flush()
	return 0
}

func cmdRoleGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, rolesUsage)
		return 2
	}
	role, err := c.GetRole(ctx, args[0])
	if err != nil {
		printError(err)
		return 1
	}
	fmt.Print(renderRoleSummary(role))
	return 0
}

func renderRoleSummary(r client.Role) string {
	out := styleBold.Render("role "+r.Name) + "\n"
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-12s", "description")), styleValue.Render(r.Description))
	out += fmt.Sprintf("  %s%s\n", styleLabel.Render(fmt.Sprintf("%-12s", "permissions")), styleValue.Render(strings.Join(r.Permissions, ", ")))
	return out
}
