package main

import (
	"fmt"
	"log"
	"net/http"
)

// Product variables
var product = "Go Descendence API-Server"
var version = "v0.0.1"

// Configuration variables
// -- API-Server
var port = ":8080"

// -- Podman

// -- Postgres

// Handles requests to the root directory
func rootHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, product)
	fmt.Fprintln(w, version)
}

// Handles healthcheck requests
func healthHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Healthy")
}

func main() {
	// Create custom mux
	// Needed for preventing usage of global mux
	descendenceAPIMux := http.NewServeMux()

	// Create api handlers
	descendenceAPIMux.HandleFunc("/", rootHandler)
	descendenceAPIMux.HandleFunc("/health", healthHandler)

	// Startup server
	// As server always retuns error, when returning -> log it
	log.Fatal(http.ListenAndServe(port, descendenceAPIMux))
}
