package main

import (
	"log"
	"net/http"
	"time"

	"github.com/r3dpan/project-descendence/internal/api"
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
	// Create custom mux
	// Needed for preventing usage of global mux
	descendenceMux := http.NewServeMux()

	// Create new API server
	descendenceAPI := api.NewAPIServer(productName, productBuild, apiVersion)

	// Create api handlers
	// Rule: the most specific pattern always wins
	// A path pattern ending with '/' matches all paths beginning with that string. {$} anchors end of path.
	descendenceMux.HandleFunc("GET /{$}", descendenceAPI.RootHandler)
	descendenceMux.HandleFunc("GET /healthz", descendenceAPI.HealthHandler)

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
