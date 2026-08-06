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
	"github.com/r3dpan/project-descendence/internal/appconfig"
	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/oidc"
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
	// DATABASE_URL/PODMAN_SOCKET may come from a dedicated config file the
	// web UI's Configuration page edits (internal/appconfig) - an actual
	// environment variable of the same name always wins if set, so ops/
	// systemd EnvironmentFile= overrides still work unchanged. Neither this
	// process nor the supervisor hot-reloads the file; a change here only
	// takes effect on the next restart of both.
	configPath, err := appconfig.DefaultPath()
	if err != nil {
		log.Fatalf("Resolving config file path: %v", err)
	}
	if envPath := os.Getenv("DESCENDENCE_CONFIG_FILE"); envPath != "" {
		configPath = envPath
	}
	fileCfg, err := appconfig.Load(configPath)
	if err != nil {
		log.Fatalf("Failed loading %s: %v", configPath, err)
	}

	// Connect to Postgres
	databaseURL := appconfig.Resolve("DATABASE_URL", fileCfg.DatabaseURL)
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set (checked environment and config file)")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("Failed creating database pool: %v", err)
	}
	defer pool.Close()

	queries := store.New(pool)

	// Connect to Podman
	podmanSocket := appconfig.Resolve("PODMAN_SOCKET", fileCfg.PodmanSocket)
	if podmanSocket == "" {
		log.Fatal("PODMAN_SOCKET is not set (checked environment and config file)")
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

	// OIDC (Phase 9, task 9.4): discovery happens once, here, at startup -
	// never per-request. Discovery failure is fatal rather than degraded: a
	// server that started anyway would accept traffic against a login path
	// that can never work, which is worse than refusing to start.
	oidcIssuerURL := os.Getenv("OIDC_ISSUER_URL")
	if oidcIssuerURL == "" {
		log.Fatal("OIDC_ISSUER_URL is not set")
	}
	oidcClientID := os.Getenv("OIDC_CLIENT_ID")
	if oidcClientID == "" {
		log.Fatal("OIDC_CLIENT_ID is not set")
	}
	oidcRedirectURL := os.Getenv("OIDC_REDIRECT_URL")
	if oidcRedirectURL == "" {
		oidcRedirectURL = "http://127.0.0.1:8080/api/v1/auth/callback"
	}
	oidcConfig, err := oidc.New(context.Background(), oidc.Options{
		IssuerURL:    oidcIssuerURL,
		ClientID:     oidcClientID,
		ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		RedirectURL:  oidcRedirectURL,
		Scopes:       oidc.ParseScopes(os.Getenv("OIDC_SCOPES")),
	})
	if err != nil {
		log.Fatalf("OIDC setup failed: %v", err)
	}
	// OIDC_BOOTSTRAP_USERNAME may be empty - bootstrap then simply never
	// fires, and cmd/seed's token path is the only way to mint the first
	// admin (task 9.10).
	oidcBootstrapUsername := os.Getenv("OIDC_BOOTSTRAP_USERNAME")

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
	descendenceAPI := api.NewAPIServer(productName, productBuild, apiVersion, queries, podmanClient, logDir, logEvents, repoStore, configPath, oidcConfig, oidcBootstrapUsername)

	// Create api handlers
	// Rule: the most specific pattern always wins
	// A path pattern ending with '/' matches all paths beginning with that string. {$} anchors end of path.
	descendenceMux.HandleFunc("GET /about", descendenceAPI.RootHandler)
	descendenceMux.HandleFunc("GET /healthz", descendenceAPI.HealthHandler)
	descendenceMux.HandleFunc("GET /api/v1/whoami", descendenceAPI.RequireAuth(descendenceAPI.WhoAmIHandler))
	descendenceMux.HandleFunc("GET /api/v1/system/status", descendenceAPI.RequireAuth(descendenceAPI.SystemStatusHandler))
	descendenceMux.HandleFunc("GET /api/v1/config", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("config:read", descendenceAPI.GetConfigHandler)))
	descendenceMux.HandleFunc("PUT /api/v1/config", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("config:write", descendenceAPI.PutConfigHandler)))
	descendenceMux.HandleFunc("GET /api/v1/auth/login", descendenceAPI.LoginHandler)
	descendenceMux.HandleFunc("GET /api/v1/auth/callback", descendenceAPI.CallbackHandler)
	descendenceMux.HandleFunc("POST /api/v1/auth/logout", descendenceAPI.LogoutHandler)
	descendenceMux.HandleFunc("POST /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:trigger", descendenceAPI.CreateRunHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runs/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:read", descendenceAPI.GetRunHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runs/stats", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:read", descendenceAPI.RunStatsHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:read", descendenceAPI.ListRunsHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runs/{id}/logs", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:read", descendenceAPI.GetRunLogsHandler)))
	descendenceMux.HandleFunc("POST /api/v1/runs/{id}/cancel", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:cancel", descendenceAPI.CancelRunHandler)))

	// Repositories and jobs (Phase 3). Note what is absent: nothing creates,
	// edits or deletes a job. A job is defined by its manifest in git, so the
	// write path for one is POST .../files (task 3.7) followed by a sync -
	// PATCH exists only for `enabled`, the single field git does not own.
	descendenceMux.HandleFunc("POST /api/v1/repos", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:write", descendenceAPI.CreateRepoHandler)))
	descendenceMux.HandleFunc("GET /api/v1/repos", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:read", descendenceAPI.ListReposHandler)))
	descendenceMux.HandleFunc("GET /api/v1/repos/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:read", descendenceAPI.GetRepoHandler)))
	descendenceMux.HandleFunc("POST /api/v1/repos/{id}/sync", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:write", descendenceAPI.SyncRepoHandler)))
	descendenceMux.HandleFunc("POST /api/v1/repos/{id}/files", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:write", descendenceAPI.CreateRepoFileHandler)))
	descendenceMux.HandleFunc("GET /api/v1/repos/{id}/files/{path...}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("repos:read", descendenceAPI.GetRepoFileHandler)))
	descendenceMux.HandleFunc("GET /api/v1/jobs", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("jobs:read", descendenceAPI.ListJobsHandler)))
	descendenceMux.HandleFunc("GET /api/v1/jobs/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("jobs:read", descendenceAPI.GetJobHandler)))
	descendenceMux.HandleFunc("PATCH /api/v1/jobs/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("jobs:write", descendenceAPI.PatchJobHandler)))
	descendenceMux.HandleFunc("POST /api/v1/jobs/{id}/runs", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runs:trigger", descendenceAPI.CreateJobRunHandler)))

	// Schedules (Phase 5, decision #27). CRUD here is a plain Postgres
	// write - the supervisor's schedule-sync loop picks up the change
	// asynchronously and renders the generated systemd units. The trigger
	// endpoint is what a generated unit's ExecStart calls via the CLI.
	descendenceMux.HandleFunc("POST /api/v1/jobs/{id}/schedules", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:write", descendenceAPI.CreateScheduleHandler)))
	descendenceMux.HandleFunc("GET /api/v1/jobs/{id}/schedules", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:read", descendenceAPI.ListSchedulesByJobHandler)))
	descendenceMux.HandleFunc("GET /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:read", descendenceAPI.GetScheduleHandler)))
	descendenceMux.HandleFunc("PATCH /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:write", descendenceAPI.PatchScheduleHandler)))
	descendenceMux.HandleFunc("DELETE /api/v1/schedules/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:write", descendenceAPI.DeleteScheduleHandler)))
	descendenceMux.HandleFunc("POST /api/v1/schedules/{id}/trigger", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("schedules:trigger", descendenceAPI.TriggerScheduleHandler)))

	// Users and tokens (Phase 8) - both principals rows, admin-only
	// (users:read/users:write). Phase 9 drops POST /users (an admin-created
	// principal has no oidc_subject and could never log in) and the
	// self password-change endpoint (there is no local password); role patch
	// and revoke stay - role patch is also how a JIT-provisioned, roleless
	// OIDC principal (task 9.6) gets its first role.
	descendenceMux.HandleFunc("GET /api/v1/users", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.ListUsersHandler)))
	descendenceMux.HandleFunc("GET /api/v1/users/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.GetUserHandler)))
	descendenceMux.HandleFunc("PATCH /api/v1/users/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:write", descendenceAPI.PatchUserHandler)))
	descendenceMux.HandleFunc("DELETE /api/v1/users/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:write", descendenceAPI.RevokeUserHandler)))

	descendenceMux.HandleFunc("GET /api/v1/tokens", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.ListTokensHandler)))
	descendenceMux.HandleFunc("POST /api/v1/tokens", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:write", descendenceAPI.CreateTokenHandler)))
	descendenceMux.HandleFunc("GET /api/v1/tokens/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.GetTokenHandler)))
	descendenceMux.HandleFunc("DELETE /api/v1/tokens/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:write", descendenceAPI.RevokeTokenHandler)))

	descendenceMux.HandleFunc("GET /api/v1/roles", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.ListRolesHandler)))
	descendenceMux.HandleFunc("GET /api/v1/roles/{name}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("users:read", descendenceAPI.GetRoleHandler)))

	descendenceMux.HandleFunc("POST /api/v1/runtimes", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runtimes:write", descendenceAPI.CreateRuntimeHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runtimes", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runtimes:read", descendenceAPI.ListRuntimesHandler)))
	descendenceMux.HandleFunc("GET /api/v1/runtimes/{id}", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runtimes:read", descendenceAPI.GetRuntimeHandler)))
	descendenceMux.HandleFunc("POST /api/v1/runtimes/{id}/build", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runtimes:write", descendenceAPI.BuildRuntimeHandler)))
	descendenceMux.HandleFunc("POST /api/v1/runtimes/prune", descendenceAPI.RequireAuth(descendenceAPI.RequirePermission("runtimes:write", descendenceAPI.PruneRuntimesHandler)))

	// Web UI (Phase 7, task 7.4; root moved to the SPA in Phase 9, task
	// 9.9). Registered last but wins for nothing /api/v1/*, /healthz or
	// /about already claim - Go 1.22's mux always picks the most specific
	// matching pattern regardless of registration order, so this catch-all
	// serves "/" itself now (there is no more exact "GET /{$}" route to
	// out-rank it) along with the SPA's client-side routes (e.g. /login,
	// /runs/42). Machine clients that want JSON server info now use
	// GET /about instead of "/".
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
