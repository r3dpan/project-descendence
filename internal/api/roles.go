package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/r3dpan/project-descendence/internal/store"
)

// --- Request/response objects ---

type roleResponse struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type roleListResponse struct {
	Items []roleResponse `json:"items"`
}

func (s *APIServer) toRoleResponse(ctx context.Context, role store.Role) (roleResponse, error) {
	keys, err := s.queries.ListRolePermissionKeys(ctx, role.ID)
	if err != nil {
		return roleResponse{}, err
	}
	return roleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		Permissions: keys,
	}, nil
}

// --- Handlers ---

// ListRolesHandler lists the three fixed built-in roles and their
// permissions (task 8.4). Read-only - decision #30 rules out an
// admin-editable custom-role builder, so there is no create/edit/delete
// here, only what powers the CLI's/web UI's role dropdown.
func (s *APIServer) ListRolesHandler(w http.ResponseWriter, r *http.Request) {
	roles, err := s.queries.ListRoles(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed listing roles")
		return
	}
	items := make([]roleResponse, len(roles))
	for i, role := range roles {
		resp, err := s.toRoleResponse(r.Context(), role)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "failed loading role permissions")
			return
		}
		items[i] = resp
	}
	writeJSON(w, http.StatusOK, roleListResponse{Items: items})
}

// GetRoleHandler returns one role by name.
func (s *APIServer) GetRoleHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	role, err := s.queries.GetRoleByName(r.Context(), name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "no role with this name")
			return
		}
		writeProblem(w, http.StatusInternalServerError, "failed loading role")
		return
	}
	resp, err := s.toRoleResponse(r.Context(), role)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed loading role permissions")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
