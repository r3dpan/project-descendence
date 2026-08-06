package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"

	"github.com/r3dpan/project-descendence/internal/gitrepo"
	"github.com/r3dpan/project-descendence/internal/logstream"
	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/store"
)

// --- API server object ---
type APIServer struct {
	productName  string
	productBuild string
	apiVersion   string

	queries *store.Queries
	podman  *podman.Client

	// logDir is where the supervisor writes run output. The API only ever
	// reads it (ARCHITECTURE.md §4.1, decision #19).
	logDir string
	// logEvents wakes streaming handlers when a run has more output or has
	// finished. Fed by the Postgres listener, not by the supervisor directly
	// - the two processes never talk (§3).
	logEvents *logstream.Broker

	// repos holds the bare git repositories that job definitions live in
	// (§4.5). The API is their sole *writer* - it creates them, commits to
	// them and scans them - which is the mirror image of logDir above, where
	// the supervisor writes and the API reads.
	repos *gitrepo.Store
}

// --- API server constructor ---
func NewAPIServer(productName string, productBuild string, apiVersion string, queries *store.Queries, podmanClient *podman.Client, logDir string, logEvents *logstream.Broker, repos *gitrepo.Store) *APIServer {
	return &APIServer{
		productName:  productName,
		productBuild: productBuild,
		apiVersion:   apiVersion,

		queries: queries,
		podman:  podmanClient,

		logDir:    logDir,
		logEvents: logEvents,
		repos:     repos,
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
	PodmanUp     bool   `json:"podmanUp"`
}

// RFC 9457 problem details, used for all error responses.
type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
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

// Handle error responses (RFC 9457 application/problem+json, ARCHITECTURE.md §4.9)
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	problem := problemDetails{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		log.Printf("Failed encoding problem+json: %v", err)
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

	// Same idea for Podman: a real /libpod/info call, not just a configured socket path
	_, err = s.podman.Info(r.Context())
	podmanUp := err == nil
	if err != nil {
		log.Printf("Health check: podman info failed: %v", err)
	}

	healthStatus := "Healthy"
	status := http.StatusOK
	if !databaseUp || !podmanUp {
		healthStatus = "Unhealthy"
		status = http.StatusServiceUnavailable
	}

	// Serialize response data
	serverHealthData := serverHealth{
		HealthStatus: healthStatus,
		DatabaseUp:   databaseUp,
		PodmanUp:     podmanUp,
	}

	writeJSON(w, status, serverHealthData)
}

// The authenticated principal, as resolved by RequireAuth. Role and
// Permissions replace the old flat Scopes field (Phase 8) - Permissions is
// what the web UI and CLI use to decide what to render (e.g. hide "New
// User") without a second round-trip.
type whoamiResponse struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// toWhoamiResponse assembles the identity response shared by WhoAmIHandler
// and LoginHandler - both need "who is this, and what can they do" in the
// same shape.
func (s *APIServer) toWhoamiResponse(ctx context.Context, principal store.Principal, perms permissionSet) (whoamiResponse, error) {
	role, err := s.queries.GetPrincipalRoleName(ctx, principal.ID)
	if err != nil {
		return whoamiResponse{}, err
	}

	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return whoamiResponse{
		ID:          principal.ID,
		Name:        principal.Name,
		Kind:        principal.Kind,
		Role:        role,
		Permissions: keys,
	}, nil
}

// Handles identity calls. Registered behind RequireAuth - proves the auth
// middleware resolves a real principal end to end.
func (s *APIServer) WhoAmIHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}
	perms, ok := permissionsFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no permission set in request context")
		return
	}

	resp, err := s.toWhoamiResponse(r.Context(), principal, perms)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed resolving principal's role")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
