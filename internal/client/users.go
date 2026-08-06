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

type CreateUserParams struct {
	Name     string  `json:"name"`
	Password *string `json:"password,omitempty"`
	Role     string  `json:"role"`
}

// CreateUserResult carries the generated plaintext password exactly once -
// not retrievable again after this response.
type CreateUserResult struct {
	User
	Password string `json:"password"`
}

// UpdateUserParams is role reassignment only, matching UserPatch's shape.
type UpdateUserParams struct {
	Role *string `json:"role,omitempty"`
}

type ChangeOwnPasswordParams struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
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

func (c *Client) CreateUser(ctx context.Context, params CreateUserParams) (CreateUserResult, error) {
	var result CreateUserResult
	err := c.do(ctx, http.MethodPost, "/api/v1/users", requestOptions{body: params}, &result)
	return result, err
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

// ChangeOwnPassword changes the calling principal's own password -
// self-service, no other principal's password is reachable this way.
func (c *Client) ChangeOwnPassword(ctx context.Context, params ChangeOwnPasswordParams) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/users/me/password", requestOptions{body: params}, nil)
}
