package main

import (
	"log"
	"net/http"

	"github.com/r3dpan/project-descendence/internal/api"
)

// Product variables
var productName = "Go Descendence API-Server"
var productVersion = "v0.0.1"
var apiVersion = "v0.0.1"

// Configuration variables
// -- API-Server
var port = ":8080"

// -- Podman

// -- Postgres

func main() {
	// Create custom mux
	// Needed for preventing usage of global mux
	descendenceMux := http.NewServeMux()

	// Create new API server
	descendenceAPI := api.NewAPIServer(productName, productVersion, apiVersion)

	// Create api handlers
	descendenceMux.HandleFunc("/", descendenceAPI.RootHandler)
	descendenceMux.HandleFunc("/healthz", descendenceAPI.HealthHandler)

	// Startup server
	// As server always returns error, when returning -> log it
	log.Fatal(http.ListenAndServe(port, descendenceMux))
}
