package main

import (
	"context"
	"fmt"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/manifest"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// materialiseScript places a job run's script inside its container (task 3.5).
//
// Called between create and start: a container's filesystem exists from
// creation, and doing this after start would race the entrypoint against the
// file it is meant to execute. Nothing is written to the host - the script
// goes from a git blob, through a tar built in memory, into the container.
//
// An ad-hoc run has no job and no script; it carries its own argv and image,
// and this is a no-op for it.
//
// # Everything is read at the run's pinned commit, never at HEAD
//
// The run recorded a commit SHA when it was created, and this reads the
// manifest and the script *at that SHA*. Re-reading rather than trusting the
// `jobs` projection is what makes a past run explainable: the projection
// tracks HEAD and may already describe a different script, whereas the SHA on
// the run cannot change. The API resolved the same SHA when it built the
// run's argv, so the two cannot disagree about what is being executed.
//
// The one thing taken from the projection is `manifest_path`, and that is safe
// because it is immutable for a given job row: a job is keyed on
// (repo_id, manifest_path), so moving a manifest does not edit a job, it
// soft-deletes one and creates another.
func materialiseScript(
	ctx context.Context,
	queries *store.Queries,
	podmanClient *podman.Client,
	repoStore *gitrepo.Store,
	run store.Run,
	containerID string,
) error {
	if !run.JobID.Valid {
		return nil
	}
	if !run.CommitSha.Valid || run.CommitSha.String == "" {
		return fmt.Errorf("run has a job but no commit SHA, so there is no version of the script to run")
	}

	job, err := queries.GetJob(ctx, run.JobID.Int64)
	if err != nil {
		return fmt.Errorf("loading job %d: %w", run.JobID.Int64, err)
	}

	repo, err := queries.GetRepo(ctx, job.RepoID)
	if err != nil {
		return fmt.Errorf("loading repository %d: %w", job.RepoID, err)
	}

	repository, err := repoStore.Open(repo.Name)
	if err != nil {
		return fmt.Errorf("opening repository %s: %w", repo.Name, err)
	}

	rawManifest, err := repository.ReadFile(run.CommitSha.String, job.ManifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest %s at %s: %w", job.ManifestPath, shortSHA(run.CommitSha.String), err)
	}
	parsed, err := manifest.Parse(job.ManifestPath, rawManifest)
	if err != nil {
		return fmt.Errorf("parsing manifest at %s: %w", shortSHA(run.CommitSha.String), err)
	}

	script, err := repository.ReadFile(run.CommitSha.String, parsed.ScriptPath)
	if err != nil {
		return fmt.Errorf("reading script %s at %s: %w", parsed.ScriptPath, shortSHA(run.CommitSha.String), err)
	}

	// Mode 0755, and the tar path is the container path with its leading
	// slash removed, since a tar entry is relative to where it is unpacked.
	containerPath := manifest.ContainerScriptPath(parsed.ScriptPath)
	archive, err := podman.TarFile(containerPath[1:], 0o755, script)
	if err != nil {
		return fmt.Errorf("building archive for %s: %w", containerPath, err)
	}

	if err := podmanClient.PutArchive(ctx, containerID, "/", archive); err != nil {
		return fmt.Errorf("copying %s into the container: %w", containerPath, err)
	}

	return nil
}

// shortSHA trims a commit SHA for log lines and failure reasons, which are
// read by people.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
