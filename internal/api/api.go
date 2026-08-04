package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/r3dpan/project-descendence/internal/store"
)

// --- API server object ---
type APIServer struct {
	productName  string
	productBuild string
	apiVersion   string

	queries *store.Queries
}

// --- API server constructor ---
func NewAPIServer(productName string, productBuild string, apiVersion string, queries *store.Queries) *APIServer {
	return &APIServer{
		productName:  productName,
		productBuild: productBuild,
		apiVersion:   apiVersion,

		queries: queries,
	}
}

// --- Response objects ---
// General server info
type serverInfo struct {
	ProductName  string `json:"productName"`
	ProductBuild string `json:"productBuild"`
	APIVersion   string `json:"apiVersion"`
}

// Server health
type serverHealth struct {
	HealthStatus string `json:"healthStatus"`
	DatabaseUp   bool   `json:"databaseUp"`
}

// --- Helper functions ---
// Handle JSON
func writeJSON(w http.ResponseWriter, status int, data any) {
	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	// Set response status line
	w.WriteHeader(status)

	// Set response body - Encoded to JSON
	// Error handling needed as answer is already sent when status is checked -> client already received it
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed encoding JSON: %v", err)
	}
}

// --- Response handlers ---
// Handles root directory calls
func (s *APIServer) RootHandler(w http.ResponseWriter, r *http.Request) {
	// Serialize response data
	serverInfoData := serverInfo{
		ProductName:  s.productName,
		ProductBuild: s.productBuild,
		APIVersion:   s.apiVersion,
	}

	writeJSON(w, http.StatusOK, serverInfoData)
}

// Handles healthcheck calls
func (s *APIServer) HealthHandler(w http.ResponseWriter, r *http.Request) {
	// Ping the database to prove the pool is actually reachable, not just configured
	_, err := s.queries.Ping(r.Context())
	databaseUp := err == nil
	if err != nil {
		log.Printf("Health check: database ping failed: %v", err)
	}

	healthStatus := "Healthy"
	status := http.StatusOK
	if !databaseUp {
		healthStatus = "Unhealthy"
		status = http.StatusServiceUnavailable
	}

	// Serialize response data
	serverHealthData := serverHealth{
		HealthStatus: healthStatus,
		DatabaseUp:   databaseUp,
	}

	writeJSON(w, status, serverHealthData)
}
