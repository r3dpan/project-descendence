package client

import (
	"context"
	"fmt"
	"net/http"
)

// Role mirrors the Role schema (task 8.4/8.7) - one of the three fixed
// built-in roles (decision #30). Read-only: there is no create/update/
// delete, since roles aren't admin-editable.
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type RoleList struct {
	Items []Role `json:"items"`
}

func (c *Client) ListRoles(ctx context.Context) (RoleList, error) {
	var list RoleList
	err := c.do(ctx, http.MethodGet, "/api/v1/roles", requestOptions{}, &list)
	return list, err
}

func (c *Client) GetRole(ctx context.Context, name string) (Role, error) {
	var role Role
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/roles/%s", name), requestOptions{}, &role)
	return role, err
}
