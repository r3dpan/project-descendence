package api

import (
	"encoding/json"
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

// --- Response handlers ---
// Handles root directory calls
func (s *APIServer) RootHandler(w http.ResponseWriter, r *http.Request) {
	// Set response type
	w.Header().Set("Content-Type", "application/json")

	// Serialize response data
	serverInfoData := serverInfo{s.productName, s.productBuild, s.apiVersion}

	// Encode reposne data to json
	json.NewEncoder(w).Encode(serverInfoData)
}

// Handles healthcheck calls
func (s *APIServer) HealthHandler(w http.ResponseWriter, r *http.Request) {
	// Set response type
	w.Header().Set("Content-Type", "application/json")

	// Serialize response data
	serverHealthData := serverHealth{s.healthStatus}

	// Encode reposne data to json
	json.NewEncoder(w).Encode(serverHealthData)
}
