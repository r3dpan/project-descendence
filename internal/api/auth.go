package api

import (
	"context"
	"crypto/sha256"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/r3dpan/project-descendence/internal/store"
)

type principalContextKey struct{}

// principalFromContext returns the principal RequireAuth resolved for this request.
func principalFromContext(ctx context.Context) (store.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(store.Principal)
	return principal, ok
}

// RequireAuth resolves the request's Bearer token to a principal and rejects
// the request with 401 if the token is missing, malformed, unknown, expired
// or revoked. On success it stamps the principal onto the request context.
func (s *APIServer) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}

		hash := sha256.Sum256([]byte(token))
		principal, err := s.queries.GetPrincipalByTokenHash(r.Context(), hash[:])
		if err != nil {
			if err != pgx.ErrNoRows {
				log.Printf("auth: token lookup failed: %v", err)
			}
			writeProblem(w, http.StatusUnauthorized, "unknown, expired or revoked token")
			return
		}

		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next(w, r.WithContext(ctx))
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "

	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}

	return token, true
}
