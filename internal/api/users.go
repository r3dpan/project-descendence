package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

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

// userCreateResponse carries the generated plaintext password exactly once
// - the same "shown once, never retrievable again" contract cmd/seed
// already established at the CLI, now available from the API too.
type userCreateResponse struct {
	userResponse
	Password string `json:"password"`
}

type userCreateRequest struct {
	Name     string  `json:"name"`
	Password *string `json:"password"`
	Role     string  `json:"role"`
}

// userPatchRequest is role reassignment only - name/password changes go
// through dedicated endpoints (self password-change), matching
// PatchJobHandler's narrow-field precedent: PATCH touches exactly what it
// documents.
type userPatchRequest struct {
	Role *string `json:"role"`
}

type userListResponse struct {
	Items []userResponse `json:"items"`
}

type selfPasswordChangeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
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

// CreateUserHandler mints a new kind='user' principal (task 8.2). If
// password is omitted, one is generated the same way cmd/seed's does -
// printed/returned once, never retrievable again.
func (s *APIServer) CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
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

	password := req.Password
	if password == nil {
		generated, err := generatePassword()
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "failed generating password")
			return
		}
		password = &generated
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed hashing password")
		return
	}

	principal, err := s.queries.CreateUserPrincipal(r.Context(), store.CreateUserPrincipalParams{
		Name:         req.Name,
		PasswordHash: passwordHash,
	})
	if err != nil {
		writeProblem(w, http.StatusConflict, fmt.Sprintf("failed creating user %q (name may already be in use)", req.Name))
		return
	}
	if err := s.queries.SetPrincipalRole(r.Context(), store.SetPrincipalRoleParams{
		PrincipalID: principal.ID,
		RoleID:      role.ID,
	}); err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed assigning role")
		return
	}

	resp := userCreateResponse{
		userResponse: userResponse{
			ID:        principal.ID,
			Name:      principal.Name,
			Role:      role.Name,
			CreatedAt: principal.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
		},
		Password: *password,
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/users/%d", principal.ID))
	writeJSON(w, http.StatusCreated, resp)
}

// PatchUserHandler reassigns a user's role (task 8.2).
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

// ChangeOwnPasswordHandler is the one mutating users endpoint gated by "is
// this the authenticated principal acting on itself" rather than a
// permission key (decision #5's self-service carve-out) - there is no
// users:write-self permission, and no other path exists to change a
// principal's password without users:write on someone else's account.
func (s *APIServer) ChangeOwnPasswordHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}
	if principal.Kind != "user" {
		writeProblem(w, http.StatusForbidden, "only user principals have a password")
		return
	}

	var req selfPasswordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}
	if req.NewPassword == "" {
		writeProblem(w, http.StatusBadRequest, "newPassword is required")
		return
	}
	if err := bcrypt.CompareHashAndPassword(principal.PasswordHash, []byte(req.CurrentPassword)); err != nil {
		writeProblem(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed hashing password")
		return
	}
	if _, err := s.queries.UpdatePrincipalPasswordHash(r.Context(), store.UpdatePrincipalPasswordHashParams{
		ID:           principal.ID,
		PasswordHash: newHash,
	}); err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed updating password")
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

// generatePassword mirrors cmd/seed's generatePassword: 24 hex chars,
// deliberately short of bcrypt's 72-byte input limit, typed into a login
// form rather than pasted as a bearer token.
func generatePassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
