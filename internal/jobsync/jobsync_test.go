package jobsync

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/store"
)

// Integration tests against a real Postgres, skipping cleanly when
// DATABASE_URL isn't set - the same pattern internal/store, internal/podman
// and internal/client use.
//
//	DATABASE_URL=postgres://... go test ./internal/jobsync
//
// Do not run these against a database a live supervisor is polling; the same
// warning as internal/store applies, for the same reason.

const testBranch = "main"

var testAuthor = gitrepo.Author{Name: "test", Email: "test@descendence.local"}

type fixture struct {
	queries   *store.Queries
	repoStore *gitrepo.Store
	repo      store.Repo
	git       *gitrepo.Repo
	ctx       context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("cannot create a pool: %v", err)
	}
	t.Cleanup(pool.Close)

	queries := store.New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	// A repository name unique to this test, so parallel or repeated runs do
	// not collide on the jobs_name_live_idx that job names share globally.
	name := fmt.Sprintf("t%d", time.Now().UnixNano())

	repoStore := gitrepo.NewStore(t.TempDir())
	gitRepo, err := repoStore.InitBare(name, testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	path, err := repoStore.Path(name)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	repo, err := queries.CreateRepo(ctx, store.CreateRepoParams{
		Name:          name,
		Path:          path,
		Kind:          "local",
		DefaultBranch: testBranch,
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	// Jobs reference the repo, so they go first.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM jobs WHERE repo_id = $1", repo.ID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM repos WHERE id = $1", repo.ID)
	})

	return &fixture{queries: queries, repoStore: repoStore, repo: repo, git: gitRepo, ctx: ctx}
}

func (f *fixture) commit(t *testing.T, path, content string) {
	t.Helper()
	if _, err := f.git.CommitFile(testBranch, path, []byte(content), testAuthor, "test: "+path); err != nil {
		t.Fatalf("CommitFile(%s): %v", path, err)
	}
}

func (f *fixture) sync(t *testing.T) Result {
	t.Helper()
	result, err := Sync(f.ctx, f.queries, f.repoStore, f.repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return result
}

func manifestFor(name string) string {
	return fmt.Sprintf(`apiVersion: descendence/v1
name: %s
description: a test job
script: %s.sh
image: docker.io/library/alpine:3.20
`, name, name)
}

// TestSyncOfEmptyRepositoryIsNotAnError covers the state every repository is
// in the moment it is created. A scan of it should report nothing, not fail.
func TestSyncOfEmptyRepositoryIsNotAnError(t *testing.T) {
	f := newFixture(t)

	result := f.sync(t)

	if result.CommitSHA != "" {
		t.Errorf("CommitSHA = %q, want empty for a repository with no commits", result.CommitSHA)
	}
	if len(result.Added)+len(result.Updated)+len(result.Removed)+len(result.Errors) != 0 {
		t.Errorf("scan of an empty repository reported %+v", result)
	}
}

func TestSyncDiscoversAndUpdates(t *testing.T) {
	f := newFixture(t)

	jobName := f.repo.Name + "-backup"
	f.commit(t, "scripts/"+jobName+".job.yaml", manifestFor(jobName))

	result := f.sync(t)
	if len(result.Added) != 1 || result.Added[0] != jobName {
		t.Fatalf("first sync Added = %v, want [%s]", result.Added, jobName)
	}
	if result.CommitSHA == "" {
		t.Error("CommitSHA is empty after a sync that found a manifest")
	}

	job, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}
	// script: is resolved relative to the manifest's own directory.
	if job.ScriptPath != "scripts/"+jobName+".sh" {
		t.Errorf("ScriptPath = %q", job.ScriptPath)
	}
	if job.ImageRef.String != "docker.io/library/alpine:3.20" {
		t.Errorf("ImageRef = %q", job.ImageRef.String)
	}
	if !job.Enabled {
		t.Error("a newly discovered job should be enabled")
	}

	// A second scan with nothing changed is an update, not a duplicate.
	result = f.sync(t)
	if len(result.Added) != 0 || len(result.Updated) != 1 {
		t.Fatalf("second sync = %+v, want one update and no additions", result)
	}
	after, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}
	if after.ID != job.ID {
		t.Errorf("re-syncing changed the job id from %d to %d", job.ID, after.ID)
	}
}

// TestSyncNeverTouchesEnabled is the rule that keeps pausing a job meaningful.
// If this ever fails, disabling a misbehaving job becomes something the next
// scan silently undoes.
func TestSyncNeverTouchesEnabled(t *testing.T) {
	f := newFixture(t)

	jobName := f.repo.Name + "-backup"
	f.commit(t, jobName+".job.yaml", manifestFor(jobName))
	f.sync(t)

	job, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}
	if _, err := f.queries.SetJobEnabled(f.ctx, store.SetJobEnabledParams{ID: job.ID, Enabled: false}); err != nil {
		t.Fatalf("SetJobEnabled: %v", err)
	}

	// Change the manifest so the upsert genuinely rewrites the row.
	f.commit(t, jobName+".job.yaml", strings.Replace(manifestFor(jobName), "a test job", "edited", 1))
	f.sync(t)

	after, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}
	if after.Enabled {
		t.Error("a sync re-enabled a job the operator had disabled")
	}
	if after.Description.String != "edited" {
		t.Errorf("the sync did not update the manifest-owned columns: description = %q", after.Description.String)
	}
}

// TestUnparseableManifestIsReportedNotDeleted is the other half of "never
// destructive". A typo must not remove a job - and, since names are unique
// among live jobs, must not free its name for something else to claim.
func TestUnparseableManifestIsReportedNotDeleted(t *testing.T) {
	f := newFixture(t)

	jobName := f.repo.Name + "-backup"
	manifestPath := jobName + ".job.yaml"
	f.commit(t, manifestPath, manifestFor(jobName))
	f.sync(t)

	// Break it: an unknown key, the shape a typo actually takes.
	f.commit(t, manifestPath, manifestFor(jobName)+"iamge: typo\n")
	result := f.sync(t)

	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %+v, want exactly one", result.Errors)
	}
	if result.Errors[0].Path != manifestPath {
		t.Errorf("error names %q, want %q", result.Errors[0].Path, manifestPath)
	}
	if len(result.Removed) != 0 {
		t.Errorf("an unreadable manifest removed its job: %v", result.Removed)
	}

	job, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("the job was deleted by a broken manifest: %v", err)
	}
	if job.DeletedAt.Valid {
		t.Error("the job was soft-deleted by a broken manifest")
	}
	// The last good definition survives untouched.
	if job.Description.String != "a test job" {
		t.Errorf("description = %q, want the last successfully parsed value", job.Description.String)
	}
}

// TestVanishedManifestSoftDeletesAndComesBack proves the property that makes
// soft deletion worth the complexity: a job's identity, and therefore its run
// history, survives its manifest being deleted and restored.
func TestVanishedManifestSoftDeletesAndComesBack(t *testing.T) {
	f := newFixture(t)

	jobName := f.repo.Name + "-backup"
	manifestPath := jobName + ".job.yaml"
	f.commit(t, manifestPath, manifestFor(jobName))
	f.sync(t)

	before, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName: %v", err)
	}

	// Delete the manifest by committing a tree without it. gitrepo writes
	// files only, so the removal goes through go-git directly.
	removeFile(t, f, manifestPath)
	result := f.sync(t)
	if len(result.Removed) != 1 || result.Removed[0] != jobName {
		t.Fatalf("Removed = %v, want [%s]", result.Removed, jobName)
	}

	// Gone from the live view...
	if _, err := f.queries.GetJobByName(f.ctx, jobName); err == nil {
		t.Error("a soft-deleted job is still resolvable by name")
	}
	// ...but the row, and anything pointing at it, survives.
	soft, err := f.queries.GetJob(f.ctx, before.ID)
	if err != nil {
		t.Fatalf("GetJob after soft delete: %v", err)
	}
	if !soft.DeletedAt.Valid {
		t.Error("the job was not soft-deleted")
	}

	// Bring it back.
	f.commit(t, manifestPath, manifestFor(jobName))
	result = f.sync(t)
	if len(result.Added) != 1 {
		t.Fatalf("restoring a manifest gave %+v, want one addition", result)
	}
	after, err := f.queries.GetJobByName(f.ctx, jobName)
	if err != nil {
		t.Fatalf("GetJobByName after restore: %v", err)
	}
	if after.ID != before.ID {
		t.Errorf("a restored manifest created a new job (%d) instead of resurrecting the old one (%d) - its run history is now orphaned", after.ID, before.ID)
	}
}

// removeFile commits the tree without one path.
//
// internal/gitrepo deliberately has no delete: the platform only ever adds
// files (task 3.7), and a manifest disappears because a person removed it with
// git. So this reaches for go-git directly rather than growing the production
// API to serve a test - the file genuinely has to leave the tree, since
// rewriting it to something unparseable tests the opposite behaviour.
func removeFile(t *testing.T, f *fixture, path string) {
	t.Helper()

	storage := filesystem.NewStorage(osfs.New(f.git.Path()), cache.NewObjectLRUDefault())
	repository, err := git.Open(storage, memfs.New())
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	// See gitrepo.CommitFile on why the persisted index has to be cleared
	// before a fresh in-memory worktree can be checked out into.
	if err := repository.Storer.SetIndex(&index.Index{Version: 2}); err != nil {
		t.Fatalf("resetting index: %v", err)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(testBranch),
	}); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := worktree.Remove(path); err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}
	if _, err := worktree.Commit("test: remove "+path, &git.CommitOptions{
		Author: &object.Signature{Name: testAuthor.Name, Email: testAuthor.Email, When: time.Now()},
	}); err != nil {
		t.Fatalf("committing removal: %v", err)
	}
}
