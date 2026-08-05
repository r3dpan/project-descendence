package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/r3dpan/project-descendence/internal/client"
)

const jobsUsage = `Usage: descendence jobs <subcommand> [flags]

Subcommands:
  list                 List jobs, by name
  get <name|id>        Show one job
  run <name|id>        Trigger a job and watch the run
  enable <name|id>     Let a job run again
  disable <name|id>    Stop a job from being run

Flags for run:
  -follow            Stream the run's output instead of its state
  -detach            Print the run id and exit without waiting
  -key <s>           Idempotency-Key, so a retry cannot double-trigger
  -param name=value  A parameter value, per the job's contract (repeatable).
                      See "descendence jobs get <name>" for what a job accepts.

A job is defined by its manifest in git, so there is no command here that
edits one. Change a job by committing its manifest - see "descendence repos
put" - and the job list updates with it. The one exception is enable/disable,
which is this installation's decision rather than the repository's.
`

const reposUsage = `Usage: descendence repos <subcommand> [flags]

Subcommands:
  list                          List repositories
  create <name>                 Create an empty bare repository
  sync <name|id>                Rebuild the job list from the repository
  put <name|id> <path> [file]   Commit a file, then sync

Flags for create:
  -branch <name>   Default branch (default "main")

Flags for put:
  -m <message>     Commit message

"put" reads the file's content from [file], or from stdin when it is omitted:

  descendence repos put library scripts/backup.sh ./backup.sh
  cat backup.job.yaml | descendence repos put library scripts/backup.job.yaml
`

func cmdJobs(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, jobsUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdJobsList(ctx, c, args[1:])
	case "get":
		return cmdJobsGet(ctx, c, args[1:])
	case "run":
		return cmdJobsRun(ctx, c, args[1:])
	case "enable":
		return cmdJobsSetEnabled(ctx, c, args[1:], true)
	case "disable":
		return cmdJobsSetEnabled(ctx, c, args[1:], false)
	case "help", "-h", "--help":
		fmt.Print(jobsUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown jobs subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, jobsUsage)
		return 2
	}
}

// resolveJob accepts either a job name or a numeric id, so that scripts can
// use ids and people can use names without a flag to say which.
func resolveJob(ctx context.Context, c *client.Client, ref string) (client.Job, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return c.GetJob(ctx, id)
	}
	return c.GetJobByName(ctx, ref)
}

func resolveRepo(ctx context.Context, c *client.Client, ref string) (client.Repo, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return c.GetRepo(ctx, id)
	}
	return c.GetRepoByName(ctx, ref)
}

func cmdJobsList(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("jobs list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, jobsUsage) }
	limit := fs.Int("limit", 0, "jobs per page")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Jobs are few and the list is a catalogue, so every page is followed
	// rather than stopping at the first - unlike runs, where the newest page
	// is usually all anyone wants.
	var jobs []client.Job
	params := client.ListJobsParams{Limit: int32(*limit)}
	for {
		page, err := c.ListJobs(ctx, params)
		if err != nil {
			printError(err)
			return 1
		}
		jobs = append(jobs, page.Items...)
		if page.NextCursor == nil {
			break
		}
		params.Cursor = *page.NextCursor
	}

	if len(jobs) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render("No jobs. Create a repository and commit a <name>.job.yaml into it."))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tENABLED\tIMAGE\tSCRIPT\tDESCRIPTION")
	for _, job := range jobs {
		enabled := "yes"
		if !job.Enabled {
			enabled = "no"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			job.Name, enabled, valueOrDash(job.ImageRef), job.ScriptPath, valueOrDash(job.Description))
	}
	writer.Flush()
	return 0
}

func cmdJobsGet(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, jobsUsage)
		return 2
	}

	job, err := resolveJob(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Print(renderJobSummary(job))
	return 0
}

func cmdJobsSetEnabled(ctx context.Context, c *client.Client, args []string, enabled bool) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, jobsUsage)
		return 2
	}

	job, err := resolveJob(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	updated, err := c.SetJobEnabled(ctx, job.ID, enabled)
	if err != nil {
		printError(err)
		return 1
	}

	word := "enabled"
	if !enabled {
		word = "disabled"
	}
	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Job %s is now %s.", updated.Name, word)))
	return 0
}

// cmdJobsRun triggers a job and then behaves exactly like `descendence run`,
// so a job-triggered run looks identical to any other however you started it.
func cmdJobsRun(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("jobs run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, jobsUsage) }
	follow := fs.Bool("follow", false, "stream output instead of state")
	detach := fs.Bool("detach", false, "print the run id and exit")
	key := fs.String("key", "", "Idempotency-Key")
	params := map[string]string{}
	fs.Func("param", "a parameter value, name=value (repeatable)", func(raw string) error {
		name, value, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("expected name=value, got %q", raw)
		}
		params[name] = value
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		reportFlagOrder(fs.Args(), "jobs run", jobsUsage)
		return 2
	}

	job, err := resolveJob(ctx, c, fs.Arg(0))
	if err != nil {
		printError(err)
		return 1
	}

	run, err := c.CreateJobRun(ctx, client.CreateJobRunParams{
		JobID:          job.ID,
		IdempotencyKey: *key,
		Params:         params,
	})
	if err != nil {
		printError(err)
		return 1
	}

	if *detach {
		fmt.Println(run.ID)
		return 0
	}

	// Which commit is being executed is the one thing a job run knows that an
	// ad-hoc run does not, and it is worth saying before the output starts.
	if run.CommitSHA != nil {
		fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Run %d: %s at %s", run.ID, job.Name, shortSHA(*run.CommitSHA))))
	}

	// Identical to `descendence run` from here on, deliberately: a run looks
	// the same however it was started.
	if *follow {
		return streamLogs(ctx, c, run.ID, 0, logPrinter{stdout: true, stderr: true})
	}

	final, err := watchRun(ctx, c, run)
	if err != nil {
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

// --- repos ---

func cmdRepos(ctx context.Context, c *client.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, reposUsage)
		return 2
	}

	switch args[0] {
	case "list":
		return cmdReposList(ctx, c)
	case "create":
		return cmdReposCreate(ctx, c, args[1:])
	case "sync":
		return cmdReposSync(ctx, c, args[1:])
	case "put":
		return cmdReposPut(ctx, c, args[1:])
	case "help", "-h", "--help":
		fmt.Print(reposUsage)
		return 0
	default:
		printError(fmt.Errorf("unknown repos subcommand %q", args[0]))
		fmt.Fprint(os.Stderr, reposUsage)
		return 2
	}
}

func cmdReposList(ctx context.Context, c *client.Client) int {
	var repos []client.Repo
	params := client.ListReposParams{}
	for {
		page, err := c.ListRepos(ctx, params)
		if err != nil {
			printError(err)
			return 1
		}
		repos = append(repos, page.Items...)
		if page.NextCursor == nil {
			break
		}
		params.Cursor = *page.NextCursor
	}

	if len(repos) == 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render("No repositories. Create one with: descendence repos create <name>"))
		return 0
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tBRANCH\tLAST SYNCED\tPATH")
	for _, repo := range repos {
		synced := "never"
		if repo.LastSyncedCommitSHA != nil {
			synced = shortSHA(*repo.LastSyncedCommitSHA)
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", repo.Name, repo.DefaultBranch, synced, repo.Path)
	}
	writer.Flush()
	return 0
}

func cmdReposCreate(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("repos create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, reposUsage) }
	branch := fs.String("branch", "", "default branch")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, reposUsage)
		return 2
	}

	repo, err := c.CreateRepo(ctx, client.CreateRepoParams{Name: fs.Arg(0), DefaultBranch: *branch})
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Created %s on %s at %s", repo.Name, repo.DefaultBranch, repo.Path)))
	return 0
}

func cmdReposSync(ctx context.Context, c *client.Client, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, reposUsage)
		return 2
	}

	repo, err := resolveRepo(ctx, c, args[0])
	if err != nil {
		printError(err)
		return 1
	}

	result, err := c.SyncRepo(ctx, repo.ID)
	if err != nil {
		printError(err)
		return 1
	}

	return reportSync(result)
}

// cmdReposPut commits one file and reports the sync that followed.
func cmdReposPut(ctx context.Context, c *client.Client, args []string) int {
	fs := flag.NewFlagSet("repos put", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, reposUsage) }
	message := fs.String("m", "", "commit message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		reportFlagOrder(fs.Args(), "repos put", reposUsage)
		return 2
	}

	repo, err := resolveRepo(ctx, c, fs.Arg(0))
	if err != nil {
		printError(err)
		return 1
	}

	var content []byte
	if fs.NArg() == 3 {
		content, err = os.ReadFile(fs.Arg(2))
	} else {
		content, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		printError(err)
		return 1
	}

	result, err := c.CreateRepoFile(ctx, repo.ID, client.CreateRepoFileParams{
		Path:    fs.Arg(1),
		Content: string(content),
		Message: *message,
	})
	if err != nil {
		printError(err)
		return 1
	}

	fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf("Committed %s as %s", result.Path, shortSHA(result.CommitSHA))))
	if result.Sync != nil {
		return reportSync(*result.Sync)
	}
	return 0
}

// reportSync prints what a scan did. Manifest errors go to stderr and make the
// command exit non-zero, because a manifest the platform cannot read is a job
// that silently is not there - reporting it as success would hide exactly the
// thing the operator needs to fix.
func reportSync(result client.SyncResult) int {
	for _, change := range []struct {
		label string
		names []string
	}{
		{"added", result.Added},
		{"updated", result.Updated},
		{"removed", result.Removed},
	} {
		if len(change.names) > 0 {
			fmt.Fprintf(os.Stderr, "%s\n", styleHint.Render(fmt.Sprintf("%s: %s", change.label, strings.Join(change.names, ", "))))
		}
	}

	if len(result.Errors) == 0 {
		if len(result.Added)+len(result.Updated)+len(result.Removed) == 0 {
			fmt.Fprintln(os.Stderr, styleHint.Render("Nothing changed."))
		}
		return 0
	}

	for _, manifestErr := range result.Errors {
		printError(fmt.Errorf("%s: %s", manifestErr.Path, manifestErr.Message))
	}
	return 1
}

// reportFlagOrder explains the one mistake this argument shape invites.
//
// Go's flag package stops parsing at the first non-flag argument, so
// "jobs run hello -follow" leaves -follow as a positional and the command
// sees two arguments where it wanted one. Every command here takes its flags
// first, matching `descendence run`, but a bare usage dump does not say that -
// and the user is left comparing their command against the help text looking
// for a typo that is not there.
func reportFlagOrder(args []string, command, usage string) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "--" {
			printError(fmt.Errorf("flags must come before the arguments: try %q", command+" "+arg+" ..."))
			fmt.Fprint(os.Stderr, usage)
			return
		}
	}
	fmt.Fprint(os.Stderr, usage)
}

// renderJobSummary shows one job, in the same shape as renderRunSummary so
// the two read alike.
func renderJobSummary(job client.Job) string {
	var b strings.Builder

	b.WriteString(styleBold.Render(job.Name))
	b.WriteString("  ")
	switch {
	case job.IsDeleted():
		b.WriteString(styleError.Render("deleted"))
	case !job.Enabled:
		b.WriteString(styleHint.Render("disabled"))
	default:
		b.WriteString(styleValue.Render("enabled"))
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

	if job.Description != nil {
		field("about", *job.Description)
	}
	field("manifest", job.ManifestPath)
	field("script", job.ScriptPath)
	if job.ImageRef != nil {
		field("image", *job.ImageRef)
	}
	if len(job.Command) > 0 {
		field("command", strings.Join(job.Command, " "))
	} else {
		// Saying this explicitly is worth a line: it is the difference
		// between "no command configured" and "the shebang decides".
		field("command", "(from the script's shebang)")
	}
	if job.TimeoutSeconds != nil {
		field("timeout", fmt.Sprintf("%ds", *job.TimeoutSeconds))
	}
	field("synced", shortSHA(job.SyncedCommitSHA))

	if len(job.Params) > 0 {
		b.WriteString("  ")
		b.WriteString(styleLabel.Render(fmt.Sprintf("%-10s", "params")))
		b.WriteString("\n")
		for _, p := range job.Params {
			b.WriteString("    ")
			b.WriteString(styleValue.Render(p.Name))
			b.WriteString(styleHint.Render(fmt.Sprintf(" (%s%s)", p.Type, paramSuffix(p))))
			b.WriteString("\n")
		}
	}

	if job.IsDeleted() {
		b.WriteString(styleHint.Render("  its manifest is gone from the repository; past runs still refer to it"))
		b.WriteString("\n")
	}

	return b.String()
}

// paramSuffix annotates a job's param listing (task 6.1's contract) with
// what a caller needs to know before setting --param: whether it can be
// omitted, and what it defaults to if so.
func paramSuffix(p client.JobParam) string {
	switch {
	case p.Secret || p.Type == "mount":
		return ", secret"
	case p.Default != nil:
		return fmt.Sprintf(", default %s", *p.Default)
	case p.Required:
		return ", required"
	default:
		return ", optional"
	}
}

func valueOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
