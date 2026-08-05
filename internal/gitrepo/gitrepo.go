// Package gitrepo is this platform's access to the bare git repositories that
// hold job definitions (ARCHITECTURE.md §4.5, decision #8 - go-git rather than
// shelling out to git, for in-process control).
//
// Two callers with very different appetites:
//
//   - The **api** is the sole writer. It creates repositories, commits files
//     into them (task 3.7) and scans them for manifests (task 3.4).
//   - The **supervisor** is a reader, and a narrow one: given a run's pinned
//     commit SHA it reads one manifest and one script blob (task 3.5). It
//     never writes.
//
// Both processes are handed the same GIT_REPO_DIR, which makes it the second
// shared directory in this system after RUN_LOG_DIR - see decision #19 on what
// that costs. api and supervisor still never talk to each other.
//
// Nothing here needs a working tree. Reads walk the commit's tree directly,
// and writes attach an *in-memory* worktree to the on-disk object store, so a
// bare repository stays bare and no checkout ever lands on disk.
package gitrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/go-git/go-billy/v5/osfs"
)

var (
	// ErrRepoNotFound is returned by Open for a repository that does not
	// exist on disk. A repos row can outlive its directory (someone deleted
	// it, a restore missed it), so this is a legible error rather than a
	// panic waiting to happen.
	ErrRepoNotFound = errors.New("repository not found")

	// ErrRepoExists is returned by InitBare when the directory is already
	// there. Never silently adopt it: an existing directory means either a
	// duplicate row or a name collision, and both want an operator.
	ErrRepoExists = errors.New("repository already exists")

	// ErrNoCommits distinguishes "this branch has nothing on it yet" from a
	// genuine failure. A repository created a moment ago and not yet written
	// to is the normal path, not an error state - a scan of one should report
	// zero jobs, not fail.
	ErrNoCommits = errors.New("branch has no commits")

	// ErrFileNotFound is returned by ReadFile for a path absent from the
	// tree at that commit.
	ErrFileNotFound = errors.New("file not found in commit")
)

// repoNamePattern constrains a repository name because the name becomes a
// directory name under GIT_REPO_DIR. Anything permitting "/" or ".." would let
// a repos row address a path outside the store - the filesystem equivalent of
// the shell-injection hole task 1.11 exists to prove closed.
var repoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Author is who a commit is attributed to. Task 3.7 fills this from the
// authenticated principal, so a script uploaded through the API is traceable
// to a token in the same way a run is.
type Author struct {
	Name  string
	Email string
}

// Store is a directory holding bare repositories, one per row in `repos`.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir. The directory is created on demand
// by InitBare rather than here, so constructing a Store has no side effects
// and both processes can do it at startup regardless of who writes.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the root directory this Store manages.
func (s *Store) Dir() string { return s.dir }

// Path returns the on-disk location of a repository. Exported because the
// `repos` row records it: the database stays the source of truth for where a
// repository lives, even though the layout is derivable (§2 principle 2).
func (s *Store) Path(name string) (string, error) {
	if err := ValidateRepoName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, name+".git"), nil
}

// ValidateRepoName rejects anything that cannot safely become a directory
// name. Exported so the API can reject a bad name with a 400 at the edge
// rather than discovering it here.
func ValidateRepoName(name string) error {
	if name == "" {
		return errors.New("repository name is empty")
	}
	if len(name) > 64 {
		return errors.New("repository name is longer than 64 characters")
	}
	if !repoNamePattern.MatchString(name) {
		return fmt.Errorf("repository name %q must start with a letter or digit and contain only letters, digits, dots, dashes and underscores", name)
	}
	return nil
}

// InitBare creates a new bare repository whose HEAD points at defaultBranch.
//
// go-git's PlainInit defaults HEAD to refs/heads/master; this repoints it,
// because the branch a repository's HEAD names is the branch a scan reads and
// a job run resolves against (task 3.4/3.5). Getting it wrong would mean an
// empty scan of a repository that visibly has manifests in it.
func (s *Store) InitBare(name, defaultBranch string) (*Repo, error) {
	repoPath, err := s.Path(name)
	if err != nil {
		return nil, err
	}
	if defaultBranch == "" {
		return nil, errors.New("default branch is empty")
	}

	if _, err := os.Stat(repoPath); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrRepoExists, repoPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("checking for existing repository: %w", err)
	}

	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating repository store directory: %w", err)
	}

	repository, err := git.PlainInit(repoPath, true)
	if err != nil {
		return nil, fmt.Errorf("initialising bare repository: %w", err)
	}

	head := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(defaultBranch))
	if err := repository.Storer.SetReference(head); err != nil {
		return nil, fmt.Errorf("pointing HEAD at %s: %w", defaultBranch, err)
	}

	return s.Open(name)
}

// Open returns an existing repository.
func (s *Store) Open(name string) (*Repo, error) {
	repoPath, err := s.Path(name)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(repoPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrRepoNotFound, repoPath)
	} else if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrRepoNotFound, repoPath)
	}

	// The repository is bare, so the git directory *is* repoPath. An
	// in-memory worktree is attached rather than none at all: go-git's
	// commit path (CommitFile, task 3.7) needs a worktree to stage into,
	// and an in-memory one keeps a bare repository bare - nothing is
	// checked out, and the only thing that reaches disk is objects and
	// refs. Reads ignore the worktree entirely.
	storage := filesystem.NewStorage(osfs.New(repoPath), cache.NewObjectLRUDefault())
	repository, err := git.Open(storage, memfs.New())
	if err != nil {
		return nil, fmt.Errorf("opening repository at %s: %w", repoPath, err)
	}

	return &Repo{repository: repository, path: repoPath}, nil
}

// Repo is a single bare repository.
type Repo struct {
	repository *git.Repository
	path       string
}

// Path returns the repository's location on disk.
func (r *Repo) Path() string { return r.path }

// HeadCommit resolves a branch to the commit SHA it currently points at.
//
// This is the resolution a job run pins (task 3.5). Everything downstream -
// which manifest was read, which script bytes ran - is derived from the SHA
// this returns, never from the branch again, so that moving the branch
// afterwards cannot change what an already-recorded run executed.
func (r *Repo) HeadCommit(branch string) (string, error) {
	ref, err := r.repository.Reference(plumbing.NewBranchReferenceName(branch), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", fmt.Errorf("%w: %s", ErrNoCommits, branch)
	} else if err != nil {
		return "", fmt.Errorf("resolving branch %s: %w", branch, err)
	}
	return ref.Hash().String(), nil
}

// ReadFile returns the contents of one path at one commit.
//
// The supervisor's entire git surface is this call, twice: once for the
// manifest at the run's pinned SHA and once for the script it names.
func (r *Repo) ReadFile(sha, filePath string) ([]byte, error) {
	if err := validateFilePath(filePath); err != nil {
		return nil, err
	}

	commit, err := r.commit(sha)
	if err != nil {
		return nil, err
	}

	file, err := commit.File(filePath)
	if errors.Is(err, object.ErrFileNotFound) {
		return nil, fmt.Errorf("%w: %s at %s", ErrFileNotFound, filePath, sha)
	} else if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", filePath, sha, err)
	}

	contents, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("reading contents of %s at %s: %w", filePath, sha, err)
	}
	return []byte(contents), nil
}

// ListFiles returns every path at a commit whose name ends in suffix, sorted
// by the tree's own order. Used by the scan (task 3.4) to find "*.job.yaml"
// anywhere in the repository - manifests sit beside their scripts, so there is
// no fixed directory to look in.
func (r *Repo) ListFiles(sha, suffix string) ([]string, error) {
	commit, err := r.commit(sha)
	if err != nil {
		return nil, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("reading tree at %s: %w", sha, err)
	}

	var found []string
	err = tree.Files().ForEach(func(f *object.File) error {
		if suffix == "" || strings.HasSuffix(f.Name, suffix) {
			found = append(found, f.Name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking tree at %s: %w", sha, err)
	}
	return found, nil
}

// CommitFile writes one file onto a branch and returns the new commit SHA.
//
// This is how the API acts as an editor for files git owns (§4.5), and it is
// deliberately the *only* write path: there is no way to change a job except
// by changing the manifest that defines it.
//
// The staging area is an in-memory filesystem, so a bare repository gains
// objects and a moved ref and nothing else. An unborn branch - a repository
// created a moment ago and never written to - is the normal first case, and
// produces a root commit rather than an error.
func (r *Repo) CommitFile(branch, filePath string, content []byte, author Author, message string) (string, error) {
	if err := validateFilePath(filePath); err != nil {
		return "", err
	}
	if branch == "" {
		return "", errors.New("branch is empty")
	}
	if author.Name == "" {
		return "", errors.New("commit author name is empty")
	}
	if message == "" {
		return "", errors.New("commit message is empty")
	}

	branchRef := plumbing.NewBranchReferenceName(branch)

	// HEAD has to name the branch being committed to before staging, because
	// go-git's Commit reads HEAD to find the parent and to know which ref to
	// move. For the first commit on an unborn branch this is all that is
	// needed; for an existing one the checkout below populates the worktree.
	if err := r.repository.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branchRef)); err != nil {
		return "", fmt.Errorf("pointing HEAD at %s: %w", branch, err)
	}

	worktree, err := r.repository.Worktree()
	if err != nil {
		return "", fmt.Errorf("opening in-memory worktree: %w", err)
	}

	// The index lives in the on-disk storer and survives between calls, but
	// the worktree is a fresh in-memory filesystem that starts empty. An
	// index left behind by an earlier commit therefore describes files this
	// worktree does not have, which go-git reads as unstaged changes and
	// refuses to check out over. Starting from an empty index makes the two
	// agree and says what is actually true: this worktree is scratch space
	// with no history of its own, created for the duration of one commit.
	//
	// Clearing it is not the same as forcing the checkout. Force makes go-git
	// reconcile the difference by deleting the files it thinks are stray,
	// which on an in-memory filesystem walks into pruning the root directory.
	if err := r.repository.Storer.SetIndex(&index.Index{Version: 2}); err != nil {
		return "", fmt.Errorf("resetting the staging index: %w", err)
	}

	// Populate the staging filesystem with the branch's current content, so
	// that committing one file does not delete every other file in the tree.
	// A missing branch means there is nothing to populate.
	if _, err := r.repository.Reference(branchRef, true); err == nil {
		if err := worktree.Checkout(&git.CheckoutOptions{Branch: branchRef}); err != nil {
			return "", fmt.Errorf("staging existing content of %s: %w", branch, err)
		}
	} else if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", fmt.Errorf("resolving branch %s: %w", branch, err)
	}

	if dir := path.Dir(filePath); dir != "." {
		if err := worktree.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating %s in worktree: %w", dir, err)
		}
	}

	file, err := worktree.Filesystem.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("creating %s in worktree: %w", filePath, err)
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return "", fmt.Errorf("writing %s in worktree: %w", filePath, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing %s in worktree: %w", filePath, err)
	}

	if _, err := worktree.Add(filePath); err != nil {
		return "", fmt.Errorf("staging %s: %w", filePath, err)
	}

	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  author.Name,
			Email: author.Email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("committing %s: %w", filePath, err)
	}

	return hash.String(), nil
}

// commit resolves a SHA to a commit object.
func (r *Repo) commit(sha string) (*object.Commit, error) {
	hash := plumbing.NewHash(sha)
	if hash.IsZero() {
		return nil, fmt.Errorf("%q is not a commit SHA", sha)
	}

	commit, err := r.repository.CommitObject(hash)
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return nil, fmt.Errorf("%w: commit %s", ErrFileNotFound, sha)
	} else if err != nil {
		return nil, fmt.Errorf("resolving commit %s: %w", sha, err)
	}
	return commit, nil
}

// validateFilePath rejects paths that would escape the repository or that git
// cannot represent. Paths here come from manifests and from API callers, so
// neither source is trusted.
func validateFilePath(filePath string) error {
	if filePath == "" {
		return errors.New("file path is empty")
	}
	if path.IsAbs(filePath) {
		return fmt.Errorf("file path %q must be relative to the repository root", filePath)
	}
	if filePath != path.Clean(filePath) {
		return fmt.Errorf("file path %q is not in canonical form (%q)", filePath, path.Clean(filePath))
	}
	for _, segment := range strings.Split(filePath, "/") {
		if segment == ".." {
			return fmt.Errorf("file path %q escapes the repository root", filePath)
		}
	}
	return nil
}
