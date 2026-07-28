package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// --- API server object ---
type APIServer struct {
	productName  string
	productBuild string
	apiVersion   string

	healthStatus string
}

// --- API server constructor ---
func NewAPIServer(productName string, productBuild string, apiVersion string) *APIServer {
	return &APIServer{
		productName:  productName,
		productBuild: productBuild,
		apiVersion:   apiVersion,

		healthStatus: "Healthy",
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
	// Serialize response data
	serverHealthData := serverHealth{
		HealthStatus: s.healthStatus,
	}

	writeJSON(w, http.StatusOK, serverHealthData)
}
