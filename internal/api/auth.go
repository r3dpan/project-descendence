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

// RequireAuth resolves the request's principal from either an
// "Authorization: Bearer" token (machines/CLI) or a session cookie
// (browsers, task 7.3) and rejects the request with 401 if neither resolves.
// Both paths stamp the same store.Principal onto the request context, so
// every handler downstream is oblivious to which credential type was used
// (ARCHITECTURE.md §4.10).
func (s *APIServer) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token, ok := bearerToken(r); ok {
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
			return
		}

		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			hash := sha256.Sum256([]byte(cookie.Value))
			row, err := s.queries.GetPrincipalBySessionTokenHash(r.Context(), hash[:])
			if err != nil {
				if err != pgx.ErrNoRows {
					log.Printf("auth: session lookup failed: %v", err)
				}
				writeProblem(w, http.StatusUnauthorized, "unknown or expired session")
				return
			}

			// sqlc gives the join a distinct row type from store.Principal
			// (same lesson as PLAN.md's Phase 6 session-history note on
			// secret_params_json: identical column *sets* don't imply
			// identical Go types once the query shape differs). Converting
			// explicitly here keeps principalFromContext's single
			// store.Principal type assertion honest instead of silently
			// failing it - that failure mode looks like "no principal in
			// context" (a 500), not a 401, and is easy to miss in testing.
			principal := store.Principal{
				ID:           row.ID,
				Kind:         row.Kind,
				Name:         row.Name,
				TokenHash:    row.TokenHash,
				TokenHint:    row.TokenHint,
				Scopes:       row.Scopes,
				PasswordHash: row.PasswordHash,
				CreatedAt:    row.CreatedAt,
				ExpiresAt:    row.ExpiresAt,
				RevokedAt:    row.RevokedAt,
			}

			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			next(w, r.WithContext(ctx))
			return
		}

		writeProblem(w, http.StatusUnauthorized, "missing bearer token or session cookie")
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
