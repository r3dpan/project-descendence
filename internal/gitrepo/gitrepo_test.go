package gitrepo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unlike internal/store and internal/podman, nothing here needs an external
// dependency - a bare repository in t.TempDir() is the real thing. These tests
// always run, so they are the phase's cheapest safety net.

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

const testBranch = "main"

var testAuthor = Author{Name: "test", Email: "test@descendence.local"}

// TestInitBarePointsHeadAtDefaultBranch guards the assumption the whole scan
// path rests on. go-git's PlainInit points HEAD at refs/heads/master; if the
// repointing in InitBare ever regresses, a repository would appear empty to a
// scan while visibly containing manifests - a failure that looks like "no jobs
// found" rather than like an error.
func TestInitBarePointsHeadAtDefaultBranch(t *testing.T) {
	store := newTestStore(t)

	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	// A fresh repository has no commits, and that is not an error state.
	if _, err := repo.HeadCommit(testBranch); !errors.Is(err, ErrNoCommits) {
		t.Fatalf("HeadCommit on an empty repository = %v, want ErrNoCommits", err)
	}

	sha, err := repo.CommitFile(testBranch, "first.txt", []byte("hello"), testAuthor, "first")
	if err != nil {
		t.Fatalf("CommitFile on an unborn branch: %v", err)
	}

	// The commit must be reachable from the default branch specifically -
	// this is what proves HEAD was repointed rather than defaulting.
	head, err := repo.HeadCommit(testBranch)
	if err != nil {
		t.Fatalf("HeadCommit after first commit: %v", err)
	}
	if head != sha {
		t.Fatalf("HeadCommit = %s, want the commit just written (%s)", head, sha)
	}
	if _, err := repo.HeadCommit("master"); !errors.Is(err, ErrNoCommits) {
		t.Fatalf("commit landed on master, not %s", testBranch)
	}
}

// TestBareRepositoryStaysBare is the load-bearing check for task 3.7. Commits
// are made through an in-memory worktree attached to the on-disk object store;
// if that ever silently became a real worktree, the "bare repositories on
// disk" design (§4.5) would be quietly untrue and every repository would carry
// a checkout of every script.
func TestBareRepositoryStaysBare(t *testing.T) {
	store := newTestStore(t)

	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	if _, err := repo.CommitFile(testBranch, "scripts/backup.sh", []byte("#!/bin/sh\necho hi\n"), testAuthor, "add backup"); err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	// Nothing may be checked out: no working copy of the file, and no .git
	// subdirectory (in a bare repository the directory *is* the git dir).
	if _, err := os.Stat(filepath.Join(repo.Path(), "scripts")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a working copy was checked out into the bare repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.Path(), ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("repository is not bare - it has a .git directory: %v", err)
	}
	// But the objects did land.
	if _, err := os.Stat(filepath.Join(repo.Path(), "objects")); err != nil {
		t.Errorf("objects were not written to disk: %v", err)
	}
}

// TestCommitFilePreservesOtherFiles catches the mistake that an in-memory
// worktree invites: staging into an empty filesystem and committing, which
// produces a tree containing only the file just written and silently deletes
// every other script in the repository.
func TestCommitFilePreservesOtherFiles(t *testing.T) {
	store := newTestStore(t)
	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	if _, err := repo.CommitFile(testBranch, "scripts/one.sh", []byte("one"), testAuthor, "add one"); err != nil {
		t.Fatalf("first CommitFile: %v", err)
	}
	sha, err := repo.CommitFile(testBranch, "scripts/two.sh", []byte("two"), testAuthor, "add two")
	if err != nil {
		t.Fatalf("second CommitFile: %v", err)
	}

	for _, want := range []struct{ path, content string }{
		{"scripts/one.sh", "one"},
		{"scripts/two.sh", "two"},
	} {
		got, err := repo.ReadFile(sha, want.path)
		if err != nil {
			t.Fatalf("ReadFile(%s) after a later commit: %v", want.path, err)
		}
		if string(got) != want.content {
			t.Errorf("ReadFile(%s) = %q, want %q", want.path, got, want.content)
		}
	}
}

// TestEditedFileYieldsNewShaAndOldShaStillReads is the phase's exit check in
// miniature: a changed script produces a different commit, and the previous
// commit still explains the previous run.
func TestEditedFileYieldsNewShaAndOldShaStillReads(t *testing.T) {
	store := newTestStore(t)
	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	before, err := repo.CommitFile(testBranch, "scripts/backup.sh", []byte("version one"), testAuthor, "add")
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	after, err := repo.CommitFile(testBranch, "scripts/backup.sh", []byte("version two"), testAuthor, "edit")
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	if before == after {
		t.Fatal("editing a script produced the same commit SHA")
	}

	old, err := repo.ReadFile(before, "scripts/backup.sh")
	if err != nil {
		t.Fatalf("ReadFile at the older SHA: %v", err)
	}
	if string(old) != "version one" {
		t.Errorf("the older SHA reads %q, want the content as it was then", old)
	}
	current, err := repo.ReadFile(after, "scripts/backup.sh")
	if err != nil {
		t.Fatalf("ReadFile at the newer SHA: %v", err)
	}
	if string(current) != "version two" {
		t.Errorf("the newer SHA reads %q, want the edited content", current)
	}
}

func TestListFilesFindsManifestsAnywhere(t *testing.T) {
	store := newTestStore(t)
	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	for _, p := range []string{
		"backup.job.yaml",
		"scripts/reindex.job.yaml",
		"deeply/nested/dir/vacuum.job.yaml",
		"scripts/reindex.sh",
		"README.md",
		"not-a-manifest.yaml",
	} {
		if _, err := repo.CommitFile(testBranch, p, []byte("x"), testAuthor, "add "+p); err != nil {
			t.Fatalf("CommitFile(%s): %v", p, err)
		}
	}

	sha, err := repo.HeadCommit(testBranch)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	found, err := repo.ListFiles(sha, ".job.yaml")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}

	want := map[string]bool{
		"backup.job.yaml":                   true,
		"scripts/reindex.job.yaml":          true,
		"deeply/nested/dir/vacuum.job.yaml": true,
	}
	if len(found) != len(want) {
		t.Fatalf("ListFiles found %v, want exactly the three manifests", found)
	}
	for _, f := range found {
		if !want[f] {
			t.Errorf("ListFiles returned %q, which is not a manifest", f)
		}
	}
}

func TestReadFileMissing(t *testing.T) {
	store := newTestStore(t)
	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	sha, err := repo.CommitFile(testBranch, "present.txt", []byte("x"), testAuthor, "add")
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	if _, err := repo.ReadFile(sha, "absent.txt"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("ReadFile of an absent path = %v, want ErrFileNotFound", err)
	}
}

func TestOpenAndInitErrors(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Open("never-created"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("Open of a missing repository = %v, want ErrRepoNotFound", err)
	}
	if _, err := store.InitBare("library", testBranch); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	if _, err := store.InitBare("library", testBranch); !errors.Is(err, ErrRepoExists) {
		t.Errorf("InitBare over an existing repository = %v, want ErrRepoExists", err)
	}
}

// TestRepoNameCannotEscapeTheStore is the filesystem sibling of the argv
// invariant proved at task 1.11: a name is data, and must never be able to
// address a path outside the directory the platform manages.
func TestRepoNameCannotEscapeTheStore(t *testing.T) {
	store := newTestStore(t)

	for _, name := range []string{
		"../escape",
		"nested/repo",
		"..",
		".hidden",
		"",
		"has space",
		"trailing/",
		"/absolute",
	} {
		if err := ValidateRepoName(name); err == nil {
			t.Errorf("ValidateRepoName(%q) accepted a name it must reject", name)
		}
		if _, err := store.Path(name); err == nil {
			t.Errorf("Path(%q) resolved a name it must reject", name)
		}
	}

	for _, name := range []string{"library", "my-repo", "repo_2", "a.b", "A1"} {
		if err := ValidateRepoName(name); err != nil {
			t.Errorf("ValidateRepoName(%q) rejected a valid name: %v", name, err)
		}
	}
}

// TestFilePathCannotEscapeTheRepository covers the same class for paths
// arriving from manifests and from API callers, neither of which is trusted.
func TestFilePathCannotEscapeTheRepository(t *testing.T) {
	store := newTestStore(t)
	repo, err := store.InitBare("library", testBranch)
	if err != nil {
		t.Fatalf("InitBare: %v", err)
	}

	for _, p := range []string{
		"../outside.sh",
		"scripts/../../outside.sh",
		"/etc/passwd",
		"",
		"./scripts/x.sh",
	} {
		if _, err := repo.CommitFile(testBranch, p, []byte("x"), testAuthor, "nope"); err == nil {
			t.Errorf("CommitFile(%q) accepted a path it must reject", p)
		}
		if _, err := repo.ReadFile(strings.Repeat("0", 40), p); err == nil {
			t.Errorf("ReadFile(%q) accepted a path it must reject", p)
		}
	}
}
