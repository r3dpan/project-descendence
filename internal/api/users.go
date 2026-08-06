package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/r3dpan/project-descendence/internal/store"
)

// --- Request/response objects ---

type userResponse struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	CreatedAt string  `json:"createdAt"`
	RevokedAt *string `json:"revokedAt,omitempty"`
}

// userPatchRequest is role reassignment only - there is no name/password
// field to touch anymore (Phase 9: identity and its name come from the IdP,
// there is no local password), matching PatchJobHandler's narrow-field
// precedent: PATCH touches exactly what it documents.
type userPatchRequest struct {
	Role *string `json:"role"`
}

type userListResponse struct {
	Items []userResponse `json:"items"`
}

func toUserResponse(row store.ListPrincipalsByKindWithRoleRow) userResponse {
	resp := userResponse{
		ID:        row.ID,
		Name:      row.Name,
		Role:      row.RoleName.String,
		CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
	if row.RevokedAt.Valid {
		s := row.RevokedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.RevokedAt = &s
	}
	return resp
}

func toUserResponseFromGet(row store.GetPrincipalByIDWithRoleRow) userResponse {
	resp := userResponse{
		ID:        row.ID,
		Name:      row.Name,
		Role:      row.RoleName.String,
		CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
	if row.RevokedAt.Valid {
		s := row.RevokedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.RevokedAt = &s
	}
	return resp
}

// --- Handlers ---

// ListUsersHandler lists kind='user' principals. Unpaginated - a homelab
// has a handful of users, not thousands (task 8.2).
func (s *APIServer) ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListPrincipalsByKindWithRole(r.Context(), "user")
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing users")
		return
	}
	items := make([]userResponse, len(rows))
	for i, row := range rows {
		items[i] = toUserResponse(row)
	}
	writeJSON(w, http.StatusOK, userListResponse{Items: items})
}

// GetUserHandler returns one user principal by id.
func (s *APIServer) GetUserHandler(w http.ResponseWriter, r *http.Request) {
	row, ok := s.lookupUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toUserResponseFromGet(row))
}

// PatchUserHandler reassigns a user's role (task 8.2) - this is also how an
// admin turns a JIT-provisioned, roleless OIDC principal (task 9.6) into one
// that can actually do anything; there is no create-user endpoint anymore
// (task 9.8) since an admin-created principal would have no oidc_subject and
// could never complete a login.
func (s *APIServer) PatchUserHandler(w http.ResponseWriter, r *http.Request) {
	row, ok := s.lookupUser(w, r)
	if !ok {
		return
	}

	var req userPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if req.Role == nil {
		writeJSON(w, http.StatusOK, toUserResponseFromGet(row))
		return
	}

	role, err := s.queries.GetRoleByName(r.Context(), *req.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusBadRequest, fmt.Sprintf("unknown role %q", *req.Role))
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed looking up role")
		return
	}
	if err := s.queries.SetPrincipalRole(r.Context(), store.SetPrincipalRoleParams{
		PrincipalID: row.ID,
		RoleID:      role.ID,
	}); err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed assigning role")
		return
	}

	row.RoleName.String, row.RoleName.Valid = role.Name, true
	writeJSON(w, http.StatusOK, toUserResponseFromGet(row))
}

// RevokeUserHandler soft-revokes a user principal (task 8.2). Never a hard
// delete: runs.principal_id is ON DELETE RESTRICT, so this makes the
// no-run-history case behave the same as the has-history case, instead of
// one silently succeeding and the other 500ing on a constraint violation.
func (s *APIServer) RevokeUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no user with this id")
		return
	}
	rows, err := s.queries.RevokePrincipal(r.Context(), id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed revoking user")
		return
	}
	if rows == 0 {
		writeProblem(w, http.StatusNotFound, "no user with this id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lookupUser resolves the {id} path value to a kind='user' principal,
// answering 404 itself when it cannot - the same pattern lookupJob uses.
func (s *APIServer) lookupUser(w http.ResponseWriter, r *http.Request) (store.GetPrincipalByIDWithRoleRow, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no user with this id")
		return store.GetPrincipalByIDWithRoleRow{}, false
	}
	row, err := s.queries.GetPrincipalByIDWithRole(r.Context(), id)
	if err != nil || row.Kind != "user" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusInternalServerError, "failed loading user")
			return store.GetPrincipalByIDWithRoleRow{}, false
		}
		writeProblem(w, http.StatusNotFound, "no user with this id")
		return store.GetPrincipalByIDWithRoleRow{}, false
	}
	return row, true
}
