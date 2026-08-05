// Package jobsync rebuilds the `jobs` projection from the manifests in a
// repository (task 3.4).
//
// The direction of authority is the whole point: git holds job definitions,
// and this table is a *projection* of them - derived state, regenerable at any
// time by running this again. Nothing here reads the projection to decide what
// git should contain; it only ever reads git to decide what the projection
// should contain.
//
// Two rules that look like details and are not:
//
//   - **`enabled` is never written.** It is the one fact about a job that this
//     installation owns rather than the repository. A sync that reset it would
//     make pausing a misbehaving job something the next sync silently undoes.
//   - **A manifest that fails to parse is reported, not deleted.** It is still
//     in the repository; the platform simply cannot read it. Treating an
//     unreadable manifest as an absent one would let a typo remove a job -
//     and, since removal is by name, let the next sync of a different
//     repository claim that name.
package jobsync

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/manifest"
	"github.com/r3dpan/project-descendence/internal/store"
)

// ManifestError is one manifest that could not be turned into a job, named so
// that a scan over many files says which one was at fault.
type ManifestError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Result is what a scan did, reported back to whoever asked for it. Job names
// rather than ids: a sync is something an operator reads.
type Result struct {
	// CommitSHA is the commit the projection was rebuilt from. Empty when
	// the repository has no commits yet.
	CommitSHA string `json:"commitSha"`

	Added   []string        `json:"added"`
	Updated []string        `json:"updated"`
	Removed []string        `json:"removed"`
	Errors  []ManifestError `json:"errors"`
}

// Sync rebuilds the projection for one repository and returns what changed.
//
// It is deliberately **not** wrapped in a single transaction. A manifest that
// cannot be written - two manifests claiming the same job name is the
// realistic case - is collected as an error and the rest of the scan
// continues, so one bad file cannot block every other job in the repository
// from updating. The cost is that a scan reporting errors has applied only
// part of itself; since a scan is a full rebuild and is idempotent, running it
// again after fixing the manifest converges. That trade is worth stating
// because the alternative - all-or-nothing - fails the more common case
// badly.
func Sync(ctx context.Context, queries *store.Queries, repoStore *gitrepo.Store, repo store.Repo) (Result, error) {
	// Initialised rather than left nil so the JSON carries [] instead of
	// null. A client counting `added.length` should not have to special-case
	// "nothing was added".
	result := Result{
		Added:   []string{},
		Updated: []string{},
		Removed: []string{},
		Errors:  []ManifestError{},
	}

	repository, err := repoStore.Open(repo.Name)
	if err != nil {
		return result, fmt.Errorf("opening repository %s: %w", repo.Name, err)
	}

	// What the projection holds now, before anything is written. This is how
	// "added" is told from "updated", and it must include soft-deleted rows:
	// a manifest that comes back has to be recognised as the row it already
	// owns, so that its run history survives.
	existing, err := queries.ListJobsByRepo(ctx, repo.ID)
	if err != nil {
		return result, fmt.Errorf("listing existing jobs: %w", err)
	}
	existingByPath := make(map[string]store.Job, len(existing))
	for _, job := range existing {
		existingByPath[job.ManifestPath] = job
	}

	sha, err := repository.HeadCommit(repo.DefaultBranch)
	if errors.Is(err, gitrepo.ErrNoCommits) {
		// A repository created a moment ago and never written to. Not an
		// error: it has no manifests, so it has no jobs, and any it used to
		// have are gone. Fall through with an empty manifest set.
		sha = ""
	} else if err != nil {
		return result, fmt.Errorf("resolving %s of %s: %w", repo.DefaultBranch, repo.Name, err)
	}
	result.CommitSHA = sha

	manifestPaths := []string{}
	if sha != "" {
		manifestPaths, err = repository.ListFiles(sha, manifest.Suffix)
		if err != nil {
			return result, fmt.Errorf("listing manifests: %w", err)
		}
	}
	sort.Strings(manifestPaths)

	// Every discovered manifest is kept, parseable or not - see the package
	// comment on why an unreadable manifest must not be treated as absent.
	keepPaths := make([]string, 0, len(manifestPaths))

	for _, manifestPath := range manifestPaths {
		keepPaths = append(keepPaths, manifestPath)

		raw, err := repository.ReadFile(sha, manifestPath)
		if err != nil {
			result.Errors = append(result.Errors, ManifestError{manifestPath, err.Error()})
			continue
		}

		parsed, err := manifest.Parse(manifestPath, raw)
		if err != nil {
			result.Errors = append(result.Errors, ManifestError{manifestPath, err.Error()})
			continue
		}

		// A manifest naming a runtime (task 4.6) is resolved by name here,
		// not left as a string for a handler to chase down later - the same
		// "report and skip, don't guess" rule the package comment states for
		// an unreadable manifest applies to one naming a runtime that does
		// not exist.
		var runtimeID pgtype.Int8
		if parsed.RuntimeName != "" {
			runtime, err := queries.GetRuntimeByName(ctx, parsed.RuntimeName)
			if err != nil {
				result.Errors = append(result.Errors, ManifestError{manifestPath,
					fmt.Sprintf("runtime %q is not defined; create it first with POST /api/v1/runtimes", parsed.RuntimeName)})
				continue
			}
			runtimeID = pgtype.Int8{Int64: runtime.ID, Valid: true}
		}

		job, err := queries.UpsertJob(ctx, upsertParams(repo.ID, sha, manifestPath, parsed, runtimeID))
		if err != nil {
			result.Errors = append(result.Errors, ManifestError{manifestPath, upsertErrorDetail(parsed.Name, err)})
			continue
		}

		previous, wasKnown := existingByPath[manifestPath]
		switch {
		case !wasKnown:
			result.Added = append(result.Added, job.Name)
		case previous.DeletedAt.Valid:
			// Resurrected: the manifest was deleted and has come back, and
			// the row - with its run history - was waiting for it.
			result.Added = append(result.Added, job.Name)
		default:
			result.Updated = append(result.Updated, job.Name)
		}
	}

	removed, err := queries.SoftDeleteJobsNotIn(ctx, store.SoftDeleteJobsNotInParams{
		RepoID:    repo.ID,
		KeepPaths: keepPaths,
	})
	if err != nil {
		return result, fmt.Errorf("soft-deleting vanished jobs: %w", err)
	}
	for _, job := range removed {
		result.Removed = append(result.Removed, job.Name)
	}

	syncedSHA := pgtype.Text{}
	if sha != "" {
		syncedSHA = pgtype.Text{String: sha, Valid: true}
	}
	if err := queries.MarkRepoSynced(ctx, store.MarkRepoSyncedParams{
		ID:                  repo.ID,
		LastSyncedCommitSha: syncedSHA,
	}); err != nil {
		return result, fmt.Errorf("recording sync: %w", err)
	}

	return result, nil
}

func upsertParams(repoID int64, sha, manifestPath string, parsed *manifest.Manifest, runtimeID pgtype.Int8) store.UpsertJobParams {
	params := store.UpsertJobParams{
		RepoID:          repoID,
		ManifestPath:    manifestPath,
		Name:            parsed.Name,
		ScriptPath:      parsed.ScriptPath,
		Command:         parsed.Command,
		SyncedCommitSha: sha,
		RuntimeID:       runtimeID,
	}
	if parsed.Description != "" {
		params.Description = pgtype.Text{String: parsed.Description, Valid: true}
	}
	if parsed.ImageRef != "" {
		params.ImageRef = pgtype.Text{String: parsed.ImageRef, Valid: true}
	}
	if parsed.TimeoutSeconds != nil {
		params.TimeoutSeconds = pgtype.Int4{Int32: *parsed.TimeoutSeconds, Valid: true}
	}
	return params
}

// upsertErrorDetail turns the one database error an operator will actually
// provoke into an instruction. Job names are unique among live jobs, so two
// manifests claiming the same name is a collision the platform cannot resolve
// on its own - and the raw constraint violation says nothing about which two
// files are involved or what to do about it.
func upsertErrorDetail(name string, err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "jobs_name_live_idx" {
		return fmt.Sprintf("another live job is already called %q; job names are unique across all repositories, so rename one of the two manifests", name)
	}
	return err.Error()
}
