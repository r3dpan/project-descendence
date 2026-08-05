package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/r3dpan/project-descendence/internal/api"
	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
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
	descendenceAPI := api.NewAPIServer(productName, productBuild, apiVersion, queries, podmanClient, logDir, logEvents)

	// Create api handlers
	// Rule: the most specific pattern always wins
	// A path pattern ending with '/' matches all paths beginning with that string. {$} anchors end of path.
	descendenceMux.HandleFunc("GET /{$}", descendenceAPI.RootHandler)
	descendenceMux.HandleFunc("GET /healthz", descendenceAPI.HealthHandler)
	descendenceMux.HandleFunc("GET /api/v1/whoami", descendenceAPI.RequireAuth(descendenceAPI.WhoAmIHandler))
	descendenceMux.HandleFunc("POST /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.CreateRunHandler))
	descendenceMux.HandleFunc("GET /api/v1/runs/{id}", descendenceAPI.RequireAuth(descendenceAPI.GetRunHandler))
	descendenceMux.HandleFunc("GET /api/v1/runs", descendenceAPI.RequireAuth(descendenceAPI.ListRunsHandler))

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
