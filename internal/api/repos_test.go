package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/store"
)

const testBranch = "main"

var testAuthor = gitrepo.Author{Name: "test", Email: "test@descendence.local"}

// repoFixture mirrors internal/jobsync's own test fixture: a real repository
// on disk and its row, torn down together since a job (were one to exist)
// would otherwise reference a dangling repo id.
type repoFixture struct {
	server *APIServer
	repo   store.Repo
	git    *gitrepo.Repo
}

func newRepoFixture(t *testing.T) (*repoFixture, context.Context) {
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

	// Unique per test so repeated/parallel runs never collide on-disk or on
	// the repos row.
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
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM repos WHERE id = $1", repo.ID)
	})

	server := NewAPIServer("test", "test", "test", queries, nil, t.TempDir(), nil, repoStore, "", nil, "")

	return &repoFixture{server: server, repo: repo, git: gitRepo}, ctx
}

func (f *repoFixture) request(t *testing.T, ctx context.Context, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/repos", nil).WithContext(ctx)
	r.SetPathValue("id", fmt.Sprintf("%d", f.repo.ID))
	r.SetPathValue("path", path)
	w := httptest.NewRecorder()
	f.server.GetRepoFileHandler(w, r)
	return w
}

func TestGetRepoFileReturnsContentAtHead(t *testing.T) {
	fx, ctx := newRepoFixture(t)

	const content = "apiVersion: descendence/v1\nname: j\nscript: j.sh\nimage: alpine\n"
	if _, err := fx.git.CommitFile(testBranch, "j.job.yaml", []byte(content), testAuthor, "test: add manifest"); err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	w := fx.request(t, ctx, "j.job.yaml")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var got repoFileGetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Path != "j.job.yaml" || got.Content != content {
		t.Errorf("got %+v, want path j.job.yaml and matching content", got)
	}
	if got.CommitSHA == "" {
		t.Error("commitSha is empty")
	}
}

func TestGetRepoFileMissingFile404s(t *testing.T) {
	fx, ctx := newRepoFixture(t)

	if _, err := fx.git.CommitFile(testBranch, "j.job.yaml", []byte("x"), testAuthor, "test: seed a commit"); err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	w := fx.request(t, ctx, "does-not-exist.job.yaml")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func TestGetRepoFileNoCommitsYet404s(t *testing.T) {
	fx, ctx := newRepoFixture(t)

	w := fx.request(t, ctx, "j.job.yaml")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

func TestGetRepoFileEscapingPath400s(t *testing.T) {
	fx, ctx := newRepoFixture(t)

	if _, err := fx.git.CommitFile(testBranch, "j.job.yaml", []byte("x"), testAuthor, "test: seed a commit"); err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	w := fx.request(t, ctx, "../../etc/passwd")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}
