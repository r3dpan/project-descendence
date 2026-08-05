package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/r3dpan/project-descendence/internal/client"
)

const runtimesUsage = `Usage: descendence runtime <subcommand> [flags]

Subcommands:
  list                    List runtimes
  get <name|id>           Show one runtime
  create <name>           Define a runtime (queues its first build)
  build <name|id>         Queue a rebuild and wait for it to finish
  prune                   Reclaim runtime image storage

Flags for create:
  -lang <python|powershell|node>   Required
  -base-image <ref>                Default: a curated image for -lang
  -sys-pkgs <a,b,c>                Debian packages, comma-separated
  -manifest <file>                 requirements.txt / *.psd1 / package.json content

Flags for prune:
  -older-than-days <n>   Prune unused runtimes built more than n days ago
  -id <n>                Prune this specific runtime now (repeatable)

A runtime is a built image, not something authored in git - unlike a job,
it is created and rebuilt directly through this command. A job manifest
references it by name with "runtime: <name>" instead of "image: <ref>".
`

func cmdRuntime(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, runtimesUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdRuntimeList(ctx, c, args[1:])
	case "get":
		return cmdRuntimeGet(ctx, c, args[1:])
	case "create":
		return cmdRuntimeCreate(ctx, c, args[1:])
	case "build":
		return cmdRuntimeBuild(ctx, c, args[1:])
	case "prune":
		return cmdRuntimePrune(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(runtimesUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown runtime subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, runtimesUsage)
		return 2
	}
}

// resolveRuntime accepts either a runtime name or a numeric id, matching
// resolveJob/resolveRepo's pattern.
func resolveRuntime(ctx context.Context, c *client.Client, ref string) (client.Runtime, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return c.GetRuntime(ctx, id)
	}
	return c.GetRuntimeByName(ctx, ref)
}

func cmdRuntimeList(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("runtime list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, runtimesUsage) }
	limit := fs.Int("limit", 0, "runtimes per page")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var runtimes []client.Runtime
	params := client.ListRuntimesParams{Limit: int32(*limit)}
	for {
		page, err := c.ListRuntimes(ctx, params)
		if err != nil {
			printError(err)
			return 1
		}
		runtimes = append(runtimes, page.Items...)
		if page.NextCursor == nil {
			break
		}
		params.Cursor = *page.NextCursor
	}

	if len(runtimes) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render("No runtimes. Create one with: descendence runtime create <name> -lang <lang>"))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tLANG\tSTATUS\tBASE IMAGE\tPRUNED")
	for _, runtime := range runtimes {
		pruned := "no"
		if runtime.ImagePruned {
			pruned = "yes"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			runtime.Name, runtime.Lang, runtime.BuildStatus, runtime.BaseImage, pruned)
	}
	writer.Flush()
	return 0
}

func cmdRuntimeGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, runtimesUsage)
		return 2
	}

	runtime, err := resolveRuntime(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderRuntimeSummary(runtime))
	return 0
}

func cmdRuntimeCreate(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("runtime create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, runtimesUsage) }
	lang := fs.String("lang", "", "python | powershell | node")
	baseImage := fs.String("base-image", "", "base image (default: curated for -lang)")
	sysPkgs := fs.String("sys-pkgs", "", "comma-separated Debian packages")
	manifestFile := fs.String("manifest", "", "requirements.txt / *.psd1 / package.json path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		reportFlagOrder(fs.Args(), "runtime create", runtimesUsage)
		return 2
	}
	if *lang == "" {
		printError(errors.New("-lang is required"))
		return 2
	}

	var langManifest string
	if *manifestFile != "" {
		content, err := os.ReadFile(*manifestFile)
		if err != nil {
			printError(err)
			return 1
		}
		langManifest = string(content)
	}

	var sysPackages []string
	if *sysPkgs != "" {
		sysPackages = strings.Split(*sysPkgs, ",")
	}

	runtime, err := c.CreateRuntime(ctx, client.CreateRuntimeParams{
		Name:         fs.Arg(0),
		BaseImage:    *baseImage,
		SysPackages:  sysPackages,
		Lang:         *lang,
		LangManifest: langManifest,
	})
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Created %s, build status %s", runtime.Name, runtime.BuildStatus)))
	return 0
}

// cmdRuntimeBuild queues a rebuild and polls until it leaves building,
// mirroring `descendence run`'s watch loop but over build status instead of
// a run's terminal states.
func cmdRuntimeBuild(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, runtimesUsage)
		return 2
	}

	runtime, err := resolveRuntime(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	if err := c.BuildRuntime(ctx, runtime.ID); err != nil {
		printError(err)
		return 1
	}

	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Building %s...", runtime.Name)))

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("stopped watching; %s is still building", runtime.Name)))
			return 130
		case <-ticker.C:
		}

		runtime, err = c.GetRuntime(ctx, runtime.ID)
		if err != nil {
			printError(err)
			return 1
		}
		switch runtime.BuildStatus {
		case "ready":
			fmt.Print(renderRuntimeSummary(runtime))
			return 0
		case "failed":
			fmt.Print(renderRuntimeSummary(runtime))
			return 1
		}
	}
}

func cmdRuntimePrune(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("runtime prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, runtimesUsage) }
	olderThanDays := fs.Int("older-than-days", -1, "prune unused runtimes built more than n days ago")
	var ids multiIntFlag
	fs.Var(&ids, "id", "prune this specific runtime now (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	params := client.PruneRuntimesParams{IDs: ids.values}
	if *olderThanDays >= 0 {
		if len(ids.values) > 0 {
			printError(errors.New("specify either -older-than-days or -id, not both"))
			return 2
		}
		params.OlderThanDays = olderThanDays
	} else if len(ids.values) == 0 {
		printError(errors.New("specify either -older-than-days or -id"))
		return 2
	}

	result, err := c.PruneRuntimes(ctx, params)
	if err != nil {
		printError(err)
		return 1
	}

	for _, name := range result.Pruned {
		fmt.Fprintln(os.Stderr, styleHint.Render("pruned: "+name))
	}
	for _, msg := range result.Skipped {
		fmt.Fprintln(os.Stderr, styleHint.Render("skipped: "+msg))
	}
	for _, msg := range result.Errors {
		printError(errors.New(msg))
	}
	if len(result.Errors) > 0 {
		return 1
	}
	return 0
}

// multiIntFlag collects a repeatable -id flag into a slice.
type multiIntFlag struct{ values []int64 }

func (f *multiIntFlag) String() string {
	if f == nil {
		return ""
	}
	strs := make([]string, len(f.values))
	for i, v := range f.values {
		strs[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(strs, ",")
}

func (f *multiIntFlag) Set(s string) error {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	f.values = append(f.values, v)
	return nil
}

// renderRuntimeSummary shows one runtime, in the same shape as
// renderJobSummary/renderRunSummary so all three read alike.
func renderRuntimeSummary(runtime client.Runtime) string {
	var b strings.Builder

	b.WriteString(styleBold.Render(runtime.Name))
	b.WriteString("  ")
	switch runtime.BuildStatus {
	case "ready":
		b.WriteString(styleValue.Render("ready"))
	case "failed":
		b.WriteString(styleError.Render("failed"))
	default:
		b.WriteString(styleHint.Render(runtime.BuildStatus))
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

	field("lang", runtime.Lang)
	field("base", runtime.BaseImage)
	if len(runtime.SysPackages) > 0 {
		field("sys pkgs", strings.Join(runtime.SysPackages, ", "))
	}
	if runtime.ImageDigest != nil {
		field("digest", *runtime.ImageDigest)
	}
	if runtime.BuildError != nil {
		field("error", *runtime.BuildError)
	}
	if runtime.ImagePruned {
		b.WriteString(styleHint.Render("  its image has been pruned; rebuild before running a job against it"))
		b.WriteString("\n")
	}

	return b.String()
}
