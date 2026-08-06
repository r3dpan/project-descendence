package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"

	"github.com/r3dpan/project-descendence/internal/store"
)

// sessionCookieName is also what the browser sends back on every subsequent
// request; RequireAuth reads it under this same name. Unchanged by Phase 9
// (decision #29): only how a session gets minted changes, not how
// RequireAuth checks one.
const sessionCookieName = "descendence_session"

// sessionTTL is deliberately generous (this is a homelab SPA, not a bank) -
// short-lived sessions just mean re-authenticating against the IdP more
// often with no real security upside at this trust level.
const sessionTTL = 30 * 24 * time.Hour

// oauthCookieTTL bounds how long a login attempt has to complete the
// redirect round-trip to the IdP and back before its state/nonce/PKCE
// verifier cookies expire.
const oauthCookieTTL = 5 * time.Minute

const (
	oauthStateCookieName    = "descendence_oauth_state"
	oauthNonceCookieName    = "descendence_oauth_nonce"
	oauthVerifierCookieName = "descendence_oauth_verifier"
)

// setOAuthCookie centralizes the short-lived, Lax cookies LoginHandler sets
// and CallbackHandler reads and clears. Lax, not Strict: the IdP returns via
// a top-level navigation (302 back to our callback), and a Strict cookie
// would not be sent on that first request (task 9.5).
func setOAuthCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/api/v1/auth/callback",
		MaxAge:   int(oauthCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearOAuthCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/api/v1/auth/callback",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// LoginHandler starts the OIDC authorization code flow with PKCE (task 9.5).
// It is a browser navigation target, not a JSON API - the SPA links here
// rather than fetching it, since an XHR cannot follow a redirect to a
// third-party origin.
func (s *APIServer) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, http.StatusInternalServerError, "OIDC is not configured")
		return
	}

	state, err := randomToken()
	if err != nil {
		log.Printf("login: state generation failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed starting login")
		return
	}
	nonce, err := randomToken()
	if err != nil {
		log.Printf("login: nonce generation failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed starting login")
		return
	}
	verifier := oauth2.GenerateVerifier()

	setOAuthCookie(w, oauthStateCookieName, state)
	setOAuthCookie(w, oauthNonceCookieName, nonce)
	setOAuthCookie(w, oauthVerifierCookieName, verifier)

	authURL := s.oidc.OAuth2.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler completes the flow (task 9.6): verifies state, exchanges
// the code with PKCE, verifies the ID token and its nonce, then resolves
// (iss, sub) to a principal - minting a session for a known, non-revoked
// subject, refusing a revoked one outright, or JIT-provisioning a brand new
// roleless one. No group claim is read; role assignment is always a
// separate, local, admin action (principal_roles), which is what keeps this
// provider-agnostic.
func (s *APIServer) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, http.StatusInternalServerError, "OIDC is not configured")
		return
	}
	ctx := r.Context()

	stateCookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || stateCookie.Value == "" {
		writeProblem(w, http.StatusBadRequest, "missing or expired login attempt")
		return
	}
	nonceCookie, err := r.Cookie(oauthNonceCookieName)
	if err != nil || nonceCookie.Value == "" {
		writeProblem(w, http.StatusBadRequest, "missing or expired login attempt")
		return
	}
	verifierCookie, err := r.Cookie(oauthVerifierCookieName)
	if err != nil || verifierCookie.Value == "" {
		writeProblem(w, http.StatusBadRequest, "missing or expired login attempt")
		return
	}
	clearOAuthCookie(w, oauthStateCookieName)
	clearOAuthCookie(w, oauthNonceCookieName)
	clearOAuthCookie(w, oauthVerifierCookieName)

	if r.URL.Query().Get("state") != stateCookie.Value {
		writeProblem(w, http.StatusUnauthorized, "state mismatch")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeProblem(w, http.StatusBadRequest, fmt.Sprintf("login failed: %s", r.URL.Query().Get("error")))
		return
	}

	token, err := s.oidc.OAuth2.Exchange(ctx, code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		log.Printf("callback: code exchange failed: %v", err)
		writeProblem(w, http.StatusUnauthorized, "failed exchanging authorization code")
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "IdP response carried no id_token")
		return
	}

	idToken, err := s.oidc.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		log.Printf("callback: id_token verification failed: %v", err)
		writeProblem(w, http.StatusUnauthorized, "invalid id_token")
		return
	}

	// go-oidc parses the nonce claim onto IDToken.Nonce but deliberately does
	// not verify it itself - that is this handler's job, per its own docs.
	if idToken.Nonce != nonceCookie.Value {
		writeProblem(w, http.StatusUnauthorized, "nonce mismatch")
		return
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("callback: claims decode failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed decoding id_token claims")
		return
	}

	principal, err := s.resolveOIDCPrincipal(ctx, idToken.Issuer, idToken.Subject, claims.PreferredUsername)
	if err != nil {
		if errors.Is(err, errRevokedSubject) {
			writeProblem(w, http.StatusForbidden, "this account has been revoked")
			return
		}
		log.Printf("callback: principal resolution failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed resolving principal")
		return
	}

	if err := s.mintSession(ctx, w, principal.ID); err != nil {
		log.Printf("callback: session creation failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed creating session")
		return
	}

	if _, err := s.queries.TouchPrincipalLastLogin(ctx, principal.ID); err != nil {
		log.Printf("callback: last-login update failed: %v", err)
		// Not fatal - a missed timestamp update is not worth failing an
		// otherwise-successful login over.
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

var errRevokedSubject = errors.New("subject is revoked")

// resolveOIDCPrincipal looks up (iss, sub), refusing a revoked match and
// JIT-provisioning an unknown one (task 9.6). name comes from
// preferred_username, falling back to sub when the IdP omits it, and is
// never refreshed on later logins - a display name changing upstream is not
// worth a write on every request. Task 9.7's bootstrap-admin match happens
// in the same transaction as the JIT insert.
func (s *APIServer) resolveOIDCPrincipal(ctx context.Context, issuer, subject, preferredUsername string) (store.Principal, error) {
	oidcIssuer := pgtype.Text{String: issuer, Valid: true}
	oidcSubject := pgtype.Text{String: subject, Valid: true}

	row, err := s.queries.GetUserPrincipalByOIDCSubject(ctx, store.GetUserPrincipalByOIDCSubjectParams{
		OidcIssuer:  oidcIssuer,
		OidcSubject: oidcSubject,
	})
	if err == nil {
		if row.RevokedAt.Valid {
			return store.Principal{}, errRevokedSubject
		}
		return assemblePrincipal(row.ID, row.Kind, row.Name, row.TokenHash, row.TokenHint, row.OidcIssuer, row.OidcSubject, row.CreatedAt, row.ExpiresAt, row.RevokedAt, row.LastLoginAt), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.Principal{}, err
	}

	name := preferredUsername
	if name == "" {
		name = subject
	}

	created, err := s.createOIDCPrincipal(ctx, name, oidcIssuer, oidcSubject)
	if err != nil {
		return store.Principal{}, err
	}

	if s.oidcBootstrapUsername != "" && preferredUsername == s.oidcBootstrapUsername {
		adminRole, err := s.queries.GetRoleByName(ctx, "admin")
		if err != nil {
			return store.Principal{}, fmt.Errorf("resolving bootstrap admin role: %w", err)
		}
		if err := s.queries.SetPrincipalRole(ctx, store.SetPrincipalRoleParams{
			PrincipalID: created.ID,
			RoleID:      adminRole.ID,
		}); err != nil {
			return store.Principal{}, fmt.Errorf("assigning bootstrap admin role: %w", err)
		}
	}

	return created, nil
}

// createOIDCPrincipal handles principals.name's UNIQUE constraint: two
// distinct subjects can share a preferred_username (or collide with an
// existing token/legacy principal's name), so a collision retries once with
// a short, deterministic suffix derived from the subject rather than
// failing the login outright.
func (s *APIServer) createOIDCPrincipal(ctx context.Context, name string, oidcIssuer, oidcSubject pgtype.Text) (store.Principal, error) {
	row, err := s.queries.CreateUserPrincipalOIDC(ctx, store.CreateUserPrincipalOIDCParams{
		Name:        name,
		OidcIssuer:  oidcIssuer,
		OidcSubject: oidcSubject,
	})
	if err == nil {
		return assemblePrincipal(row.ID, row.Kind, row.Name, row.TokenHash, row.TokenHint, row.OidcIssuer, row.OidcSubject, row.CreatedAt, row.ExpiresAt, row.RevokedAt, row.LastLoginAt), nil
	}

	sum := sha512.Sum512([]byte(oidcSubject.String))
	disambiguated := fmt.Sprintf("%s-%s", name, hex.EncodeToString(sum[:4]))
	row, err = s.queries.CreateUserPrincipalOIDC(ctx, store.CreateUserPrincipalOIDCParams{
		Name:        disambiguated,
		OidcIssuer:  oidcIssuer,
		OidcSubject: oidcSubject,
	})
	if err != nil {
		return store.Principal{}, fmt.Errorf("creating JIT principal (tried %q and %q): %w", name, disambiguated, err)
	}
	return assemblePrincipal(row.ID, row.Kind, row.Name, row.TokenHash, row.TokenHint, row.OidcIssuer, row.OidcSubject, row.CreatedAt, row.ExpiresAt, row.RevokedAt, row.LastLoginAt), nil
}

// mintSession creates the session row and sets the cookie - shared by
// CallbackHandler now, exactly as LoginHandler's password flow did before
// Phase 9 (decision #29: only issuance changes, not the session mechanism).
func (s *APIServer) mintSession(ctx context.Context, w http.ResponseWriter, principalID int64) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(token))

	expiresAt := time.Now().Add(sessionTTL)
	if _, err := s.queries.CreateSession(ctx, store.CreateSessionParams{
		PrincipalID: principalID,
		TokenHash:   hash[:],
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// LogoutHandler deletes the session row outright (no soft-delete - a dead
// session has no history worth keeping) and clears the cookie.
func (s *APIServer) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		hash := sha256.Sum256([]byte(cookie.Value))
		if err := s.queries.DeleteSessionByTokenHash(r.Context(), hash[:]); err != nil {
			log.Printf("logout: session delete failed: %v", err)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
