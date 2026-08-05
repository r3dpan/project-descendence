package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/r3dpan/project-descendence/internal/api"
	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
	webdist "github.com/r3dpan/project-descendence/web"
)

// Product variables
var productName = "Go Descendence API-Server"
var productBuild = "v0.0.1"
var apiVersion = "v0.0.1"

// Configuration variables
// -- API-Server
var port = ":8080"
var readHeaderTimeout = 5 * time.Second
var readTimeout = 15 * time.Second
var writeTimeout = 30 * time.Second
var idleTimeout = 120 * time.Second

// -- Podman

// -- Postgres

// spaHandler serves the embedded SPA build, falling back to index.html for
// any path with no matching static file - a browser refresh on a
// client-side route like /runs/42 must still get the app shell, not a 404
// from http.FileServer.
func spaHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		if _, err := fs.Stat(distFS, path[1:]); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}

		fileServer.ServeHTTP(w, r)
	})
}

func main() {
	// Connect to Postgres
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("Failed creating database pool: %v", err)
	}
	defer pool.Close()

	queries := store.New(pool)

	// Connect to Podman
	podmanSocket := os.Getenv("PODMAN_SOCKET")
	if podmanSocket == "" {
		log.Fatal("PODMAN_SOCKET is not set")
	}
	podmanClient := podman.NewClient(podmanSocket)

	// Where the supervisor writes run output. The API reads the same
	// directory (never writes it), so both processes must be given the same
	// path - see ARCHITECTURE.md decision #19.
	logDir := os.Getenv("RUN_LOG_DIR")
	if logDir == "" {
		log.Fatal("RUN_LOG_DIR is not set")
	}

	// Job definitions live in bare git repositories (ARCHITECTURE.md §4.5).
	// The API is their sole writer - it creates them, commits to them and
	// scans them - while the supervisor only reads a blob at a pinned commit.
	// That is the mirror image of RUN_LOG_DIR above, and like it, both
	// processes must be given the same path.
	repoDir := os.Getenv("GIT_REPO_DIR")
	if repoDir == "" {
		log.Fatal("GIT_REPO_DIR is not set")
	}
	repoStore := gitrepo.NewStore(repoDir)

	// One Postgres listener for the whole process, fanning run events out to
	// however many clients are streaming (task 2.3). Its context is cancelled
	// on shutdown so the connection is not left behind.
	listenCtx, stopListening := context.WithCancel(context.Background())
	defer stopListening()

	logEvents := logstream.NewBroker()
	go logstream.Listen(listenCtx, databaseURL, logEvents)

	// Create custom mux
	// Needed for preventing usage of global mux
	descendenceMux := http.NewServeMux()

	// Create new API server
	descendenceAPI := api.NewAPIServer(productName, productBuild, apiVersion, queries, podmanClient, logDir, logEvents, repoStore)

	// Create api handlers
	// Rule: the most specific pattern always wins
	// A path pattern ending with '/' matches all paths beginning with that string. {$} anchors end of path.
	descendenceMux.HandleFunc("GET /{$}", descendenceAPI.RootHandler)
	descendenceMux.HandleFunc("GET /healthz", descendenceAPI.HealthHandler)
	descendenceMux.HandleFunc("GET /api/v1/whoami", descendenceAPI.RequireAuth(descendenceAPI.WhoAmIHandler))
	descendenceMux.HandleFunc("POST /api/v1/auth/login", descendenceAPI.LoginHandler)
	descendenceMux.HandleFunc("POST /api/v1/auth/logout", descendenceAPI.LogoutHandler)
	descendenceMux.HandleFunc("POST /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.CreateRunHandler))
	descendenceMux.HandleFunc("GET /api/v1/runs/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetRunHandler))
	descendenceMux.HandleFunc("GET /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.ListRunsHandler))
	descendenceMux.HandleFunc("GET /api/v1/runs/{id}/logs", descendenceAPI.RequireAuth(descendenceAPI.GetRunLogsHandler))
	descendenceMux.HandleFunc("POST /api/v1/runs/{id}/cancel", descendenceAPI.RequireAuth(descendenceAPI.CancelRunHandler))

	// Repositories and jobs (Phase 3). Note what is absent: nothing creates,
	// edits or deletes a job. A job is defined by its manifest in git, so the
	// write path for one is POST .../files (task 3.7) followed by a sync -
	// PATCH exists only for `enabled`, the single field git does not own.
	descendenceMux.HandleFunc("POST /api/v1/repos", descendenceAPI.RequireAuth(descendenceAPI.CreateRepoHandler))
	descendenceMux.HandleFunc("GET /api/v1/repos", descendenceAPI.RequireAuth(descendenceAPI.ListReposHandler))
	descendenceMux.HandleFunc("GET /api/v1/repos/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetRepoHandler))
	descendenceMux.HandleFunc("POST /api/v1/repos/{id}/sync", descendenceAPI.RequireAuth(descendenceAPI.SyncRepoHandler))
	descendenceMux.HandleFunc("POST /api/v1/repos/{id}/files", descendenceAPI.RequireAuth(descendenceAPI.CreateRepoFileHandler))
	descendenceMux.HandleFunc("GET /api/v1/repos/{id}/files/{path...}", descendenceAPI.RequireAuth(descendenceAPI.GetRepoFileHandler))
	descendenceMux.HandleFunc("GET /api/v1/jobs", descendenceAPI.RequireAuth(descendenceAPI.ListJobsHandler))
	descendenceMux.HandleFunc("GET /api/v1/jobs/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetJobHandler))
	descendenceMux.HandleFunc("PATCH /api/v1/jobs/{id}", descendenceAPI.RequireAuth(descendenceAPI.PatchJobHandler))
	descendenceMux.HandleFunc("POST /api/v1/jobs/{id}/runs", descendenceAPI.RequireAuth(descendenceAPI.CreateJobRunHandler))

	// Schedules (Phase 5, decision #27). CRUD here is a plain Postgres
	// write - the supervisor's schedule-sync loop picks up the change
	// asynchronously and renders the generated systemd units. The trigger
	// endpoint is what a generated unit's ExecStart calls via the CLI.
	descendenceMux.HandleFunc("POST /api/v1/jobs/{id}/schedules", descendenceAPI.RequireAuth(descendenceAPI.CreateScheduleHandler))
	descendenceMux.HandleFunc("GET /api/v1/jobs/{id}/schedules", descendenceAPI.RequireAuth(descendenceAPI.ListSchedulesByJobHandler))
	descendenceMux.HandleFunc("GET /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetScheduleHandler))
	descendenceMux.HandleFunc("PATCH /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.PatchScheduleHandler))
	descendenceMux.HandleFunc("DELETE /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.DeleteScheduleHandler))
	descendenceMux.HandleFunc("POST /api/v1/schedules/{id}/trigger", descendenceAPI.RequireAuth(descendenceAPI.TriggerScheduleHandler))

	descendenceMux.HandleFunc("POST /api/v1/runtimes", descendenceAPI.RequireAuth(descendenceAPI.CreateRuntimeHandler))
	descendenceMux.HandleFunc("GET /api/v1/runtimes", descendenceAPI.RequireAuth(descendenceAPI.ListRuntimesHandler))
	descendenceMux.HandleFunc("GET /api/v1/runtimes/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetRuntimeHandler))
	descendenceMux.HandleFunc("POST /api/v1/runtimes/{id}/build", descendenceAPI.RequireAuth(descendenceAPI.BuildRuntimeHandler))
	descendenceMux.HandleFunc("POST /api/v1/runtimes/prune", descendenceAPI.RequireAuth(descendenceAPI.PruneRuntimesHandler))

	// Web UI (Phase 7, task 7.4). Registered last but wins for nothing "GET
	// /{$}" or /api/v1/*, /healthz already claim - Go 1.22's mux always
	// picks the most specific matching pattern regardless of registration
	// order, so the root route above keeps returning JSON server info for
	// machine clients and this catch-all only ever serves the SPA's own
	// client-side routes (e.g. /login, /runs/42).
	distFS, err := fs.Sub(webdist.Dist, "dist")
	if err != nil {
		log.Fatalf("Failed opening embedded web/dist: %v", err)
	}
	descendenceMux.Handle("/", spaHandler(distFS))

	// Create descedence server
	descendenceServer := &http.Server{
		Addr:              port,
		Handler:           descendenceMux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Startup server
	// As server always returns error, when returning -> log it
	log.Fatal(descendenceServer.ListenAndServe())
}
