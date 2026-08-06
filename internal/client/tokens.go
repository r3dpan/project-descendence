package client

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Token mirrors the Token schema (task 8.3/8.7).
type Token struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	TokenHint string     `json:"tokenHint"`
	CreatedAt string     `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RevokedAt *string    `json:"revokedAt,omitempty"`
}

type TokenList struct {
	Items []Token `json:"items"`
}

type CreateTokenParams struct {
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// CreateTokenResult carries the plaintext token exactly once - not
// retrievable again after this response.
type CreateTokenResult struct {
	Token
	PlaintextToken string `json:"token"`
}

func (c *Client) ListTokens(ctx context.Context) (TokenList, error) {
	var list TokenList
	err := c.do(ctx, http.MethodGet, "/api/v1/tokens", requestOptions{}, &list)
	return list, err
}

func (c *Client) GetToken(ctx context.Context, id int64) (Token, error) {
	var tok Token
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/tokens/%d", id), requestOptions{}, &tok)
	return tok, err
}

func (c *Client) CreateToken(ctx context.Context, params CreateTokenParams) (CreateTokenResult, error) {
	var result CreateTokenResult
	err := c.do(ctx, http.MethodPost, "/api/v1/tokens", requestOptions{body: params}, &result)
	return result, err
}

// RevokeToken soft-revokes a token principal - never a hard delete, same
// reasoning as RevokeUser.
func (c *Client) RevokeToken(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/tokens/%d", id), requestOptions{}, nil)
}
