package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/r3dpan/project-descendence/internal/store"
)

type principalContextKey struct{}
type permissionsContextKey struct{}

// principalFromContext returns the principal RequireAuth resolved for this request.
func principalFromContext(ctx context.Context) (store.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(store.Principal)
	return principal, ok
}

// permissionSet is the resolved permission-key set (§2, Phase 8) for the
// request's principal - a plain map so RequirePermission's check is an
// in-memory lookup, not a second query per handler.
type permissionSet map[string]struct{}

func (p permissionSet) has(key string) bool {
	_, ok := p[key]
	return ok
}

func permissionsFromContext(ctx context.Context) (permissionSet, bool) {
	perms, ok := ctx.Value(permissionsContextKey{}).(permissionSet)
	return perms, ok
}

// assemblePrincipal builds the shared store.Principal from individual
// fields rather than a row struct - GetPrincipalByTokenHash and
// GetPrincipalBySessionTokenHash give sqlc distinct row types even though
// their column sets are identical (the same lesson the "Fix bearer-token
// auth" commit already learned once), and Go has no structural typing to
// convert between them generically, so this is the shared middle ground:
// one conversion each caller supplies its row's fields to.
func assemblePrincipal(id int64, kind, name string, tokenHash []byte, tokenHint pgtype.Text, passwordHash []byte, createdAt, expiresAt, revokedAt pgtype.Timestamptz) store.Principal {
	return store.Principal{
		ID:           id,
		Kind:         kind,
		Name:         name,
		TokenHash:    tokenHash,
		TokenHint:    tokenHint,
		PasswordHash: passwordHash,
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
		RevokedAt:    revokedAt,
	}
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
			row, err := s.queries.GetPrincipalByTokenHash(r.Context(), hash[:])
			if err != nil {
				if err != pgx.ErrNoRows {
					log.Printf("auth: token lookup failed: %v", err)
				}
				writeProblem(w, http.StatusUnauthorized, "unknown, expired or revoked token")
				return
			}

			// sqlc gives this query its own row type distinct from
			// store.Principal, the same lesson as GetPrincipalBySessionTokenHash
			// below - assemblePrincipal converts explicitly here so
			// principalFromContext's single store.Principal type assertion stays
			// honest instead of silently failing it (a 500 that looks like "no
			// principal in context", not a 401 - this was live and unnoticed
			// since Phase 7.1-7.5's sqlc regen gave this query the same
			// distinct-row treatment).
			principal := assemblePrincipal(row.ID, row.Kind, row.Name, row.TokenHash, row.TokenHint, row.PasswordHash, row.CreatedAt, row.ExpiresAt, row.RevokedAt)

			perms, err := s.resolvePermissions(r.Context(), principal.ID)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "failed resolving principal's permissions")
				return
			}

			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			ctx = context.WithValue(ctx, permissionsContextKey{}, perms)
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
			// identical Go types once the query shape differs). assemblePrincipal
			// converts explicitly here so principalFromContext's single
			// store.Principal type assertion stays honest instead of silently
			// failing it - that failure mode looks like "no principal in
			// context" (a 500), not a 401, and is easy to miss in testing.
			principal := assemblePrincipal(row.ID, row.Kind, row.Name, row.TokenHash, row.TokenHint, row.PasswordHash, row.CreatedAt, row.ExpiresAt, row.RevokedAt)

			perms, err := s.resolvePermissions(r.Context(), principal.ID)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "failed resolving principal's permissions")
				return
			}

			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			ctx = context.WithValue(ctx, permissionsContextKey{}, perms)
			next(w, r.WithContext(ctx))
			return
		}

		writeProblem(w, http.StatusUnauthorized, "missing bearer token or session cookie")
	}
}

// resolvePermissions loads a principal's effective permission set via the
// principal_roles -> role_permissions -> permissions join (one indexed
// query, not one query per handler).
func (s *APIServer) resolvePermissions(ctx context.Context, principalID int64) (permissionSet, error) {
	keys, err := s.queries.GetPrincipalPermissions(ctx, principalID)
	if err != nil {
		return nil, err
	}
	perms := make(permissionSet, len(keys))
	for _, k := range keys {
		perms[k] = struct{}{}
	}
	return perms, nil
}

// RequirePermission wraps a handler (composed after RequireAuth in the
// route table, e.g. RequireAuth(RequirePermission("jobs:write", handler)))
// so authorization stays out of handler bodies - the same reasoning that
// keeps RequireAuth itself invisible to handlers, and a design chosen over
// inline checks (the deleted principalHasScope) because it scales to
// permissions-times-handlers without every handler growing an
// almost-identical if-block (Phase 8, ARCHITECTURE.md §6 decision #30).
func (s *APIServer) RequirePermission(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		perms, ok := permissionsFromContext(r.Context())
		if !ok {
			writeProblem(w, http.StatusInternalServerError, "no permission set in request context")
			return
		}
		if !perms.has(perm) {
			writeProblem(w, http.StatusForbidden, fmt.Sprintf("principal is missing the %q permission", perm))
			return
		}
		next(w, r)
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
