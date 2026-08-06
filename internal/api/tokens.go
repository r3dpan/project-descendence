package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/store"
)

// --- Request/response objects ---

type tokenResponse struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	TokenHint string     `json:"tokenHint,omitempty"`
	CreatedAt string     `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RevokedAt *string    `json:"revokedAt,omitempty"`
}

// tokenCreateResponse carries the plaintext token exactly once - shown
// once, never retrievable again, same contract as cmd/seed and the users
// API's generated-password response.
type tokenCreateResponse struct {
	tokenResponse
	Token string `json:"token"`
}

type tokenCreateRequest struct {
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type tokenListResponse struct {
	Items []tokenResponse `json:"items"`
}

func toTokenResponse(row store.ListPrincipalsByKindWithRoleRow) tokenResponse {
	resp := tokenResponse{
		ID:        row.ID,
		Name:      row.Name,
		Role:      row.RoleName.String,
		TokenHint: row.TokenHint.String,
		CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
	if row.ExpiresAt.Valid {
		resp.ExpiresAt = &row.ExpiresAt.Time
	}
	if row.RevokedAt.Valid {
		s := row.RevokedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.RevokedAt = &s
	}
	return resp
}

func toTokenResponseFromGet(row store.GetPrincipalByIDWithRoleRow) tokenResponse {
	resp := tokenResponse{
		ID:        row.ID,
		Name:      row.Name,
		Role:      row.RoleName.String,
		TokenHint: row.TokenHint.String,
		CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
	if row.ExpiresAt.Valid {
		resp.ExpiresAt = &row.ExpiresAt.Time
	}
	if row.RevokedAt.Valid {
		s := row.RevokedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.RevokedAt = &s
	}
	return resp
}

// --- Handlers ---

// ListTokensHandler lists kind='token' principals. Unpaginated, same
// reasoning as ListUsersHandler (task 8.3).
func (s *APIServer) ListTokensHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListPrincipalsByKindWithRole(r.Context(), "token")
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing tokens")
		return
	}
	items := make([]tokenResponse, len(rows))
	for i, row := range rows {
		items[i] = toTokenResponse(row)
	}
	writeJSON(w, http.StatusOK, tokenListResponse{Items: items})
}

// GetTokenHandler returns one token principal by id.
func (s *APIServer) GetTokenHandler(w http.ResponseWriter, r *http.Request) {
	row, ok := s.lookupToken(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toTokenResponseFromGet(row))
}

// CreateTokenHandler mints a new kind='token' principal (task 8.3) - the
// API-driven equivalent of `cmd/seed -kind token`, closing the gap that
// tool minting was previously the only way to create one.
func (s *APIServer) CreateTokenHandler(w http.ResponseWriter, r *http.Request) {
	var req tokenCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if req.Name == "" {
		writeProblem(w, http.StatusBadRequest, "name is required")
		return
	}
	role, err := s.queries.GetRoleByName(r.Context(), req.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("unknown role %q", req.Role))
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed looking up role")
		return
	}

	token, err := generateToken()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed generating token")
		return
	}
	hash := sha256.Sum256([]byte(token))

	var expiresAt pgtype.Timestamptz
	if req.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *req.ExpiresAt, Valid: true}
	}

	principal, err := s.queries.CreateTokenPrincipal(r.Context(), store.CreateTokenPrincipalParams{
		Name:      req.Name,
		TokenHash: hash[:],
		TokenHint: pgtype.Text{String: token[len(token)-8:], Valid: true},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("failed creating token %q (name may already be in use)", req.Name))
		return
	}
	if err := s.queries.SetPrincipalRole(r.Context(), store.SetPrincipalRoleParams{
		PrincipalID: principal.ID,
		RoleID:      role.ID,
	}); err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed assigning role")
		return
	}

	resp := tokenCreateResponse{
		tokenResponse: tokenResponse{
			ID:        principal.ID,
			Name:      principal.Name,
			Role:      role.Name,
			TokenHint: principal.TokenHint.String,
			CreatedAt: principal.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
		},
		Token: token,
	}
	if principal.ExpiresAt.Valid {
		resp.ExpiresAt = &principal.ExpiresAt.Time
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/tokens/%d", principal.ID))
	writeJSON(w, http.StatusCreated, resp)
}

// RevokeTokenHandler soft-revokes a token principal (task 8.3) - same
// reasoning as RevokeUserHandler: runs.principal_id is ON DELETE RESTRICT,
// so a revoked token simply stops authenticating rather than being deleted.
func (s *APIServer) RevokeTokenHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no token with this id")
		return
	}
	rows, err := s.queries.RevokePrincipal(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed revoking token")
		return
	}
	if rows == 0 {
		writeProblem(w, http.StatusNotFound, "no token with this id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lookupToken resolves the {id} path value to a kind='token' principal,
// answering 404 itself when it cannot.
func (s *APIServer) lookupToken(w http.ResponseWriter, r *http.Request) (store.GetPrincipalByIDWithRoleRow, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no token with this id")
		return store.GetPrincipalByIDWithRoleRow{}, false
	}
	row, err := s.queries.GetPrincipalByIDWithRole(r.Context(), id)
	if err != nil || row.Kind != "token" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusInternalServerError, "failed loading token")
			return store.GetPrincipalByIDWithRoleRow{}, false
		}
		writeProblem(w, http.StatusNotFound, "no token with this id")
		return store.GetPrincipalByIDWithRoleRow{}, false
	}
	return row, true
}

// generateToken mirrors cmd/seed's generateToken: sra_live_<64 hex chars>,
// per ARCHITECTURE.md §4.10.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sra_live_" + hex.EncodeToString(buf), nil
}
