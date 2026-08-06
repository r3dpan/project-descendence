package client

import (
	"context"
	"fmt"
	"net/http"
)

// User mirrors the User schema (task 8.2/8.7).
type User struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	CreatedAt string  `json:"createdAt"`
	RevokedAt *string `json:"revokedAt,omitempty"`
}

type UserList struct {
	Items []User `json:"items"`
}

// UpdateUserParams is role reassignment only, matching UserPatch's shape.
// This is also how a JIT-provisioned, roleless OIDC principal (task 9.6)
// gets its first role - there is no create-user endpoint anymore (task 9.8):
// an admin-created principal would have no oidc_subject and could never log
// in.
type UpdateUserParams struct {
	Role *string `json:"role,omitempty"`
}

func (c *Client) ListUsers(ctx context.Context) (UserList, error) {
	var list UserList
	err := c.do(ctx, http.MethodGet, "/api/v1/users", requestOptions{}, &list)
	return list, err
}

func (c *Client) GetUser(ctx context.Context, id int64) (User, error) {
	var user User
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", id), requestOptions{}, &user)
	return user, err
}

func (c *Client) UpdateUser(ctx context.Context, id int64, params UpdateUserParams) (User, error) {
	var user User
	err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", id), requestOptions{body: params}, &user)
	return user, err
}

// RevokeUser soft-revokes a user principal - never a hard delete, matching
// the server's DELETE semantics (runs.principal_id is ON DELETE RESTRICT).
func (c *Client) RevokeUser(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", id), requestOptions{}, nil)
}
