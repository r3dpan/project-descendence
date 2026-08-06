package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/r3dpan/project-descendence/internal/store"
)

// sessionCookieName is also what the browser sends back on every subsequent
// request; RequireAuth reads it under this same name.
const sessionCookieName = "descendence_session"

// sessionTTL is deliberately generous (Phase 7 is a homelab SPA, not a bank)
// - short-lived sessions just mean re-typing a password more often with no
// real security upside at this trust level.
const sessionTTL = 30 * 24 * time.Hour

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginHandler checks a username+password against a kind='user' principal
// (task 7.3 - ARCHITECTURE.md §4.10's "a login form against a local account
// is fine first", ahead of OIDC) and, on success, mints a session and sets
// it as an HttpOnly/Secure/SameSite=Lax cookie so a same-origin SPA never
// touches the token directly (never localStorage - §4.10).
func (s *APIServer) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeProblem(w, http.StatusBadRequest, "username and password are required")
		return
	}

	principal, err := s.queries.GetUserPrincipalByName(r.Context(), req.Username)
	if err != nil {
		if err != pgx.ErrNoRows {
			log.Printf("login: principal lookup failed: %v", err)
		}
		// Same response whether the username doesn't exist or the password
		// is wrong - don't tell an attacker which half was correct. The
		// bcrypt compare below still runs on a fabricated hash in the
		// not-found case to keep the timing profile the same either way.
		bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), []byte(req.Password))
		writeProblem(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword(principal.PasswordHash, []byte(req.Password)); err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	token, err := generateSessionToken()
	if err != nil {
		log.Printf("login: session token generation failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed creating session")
		return
	}
	hash := sha256.Sum256([]byte(token))

	expiresAt := time.Now().Add(sessionTTL)
	if _, err := s.queries.CreateSession(r.Context(), store.CreateSessionParams{
		PrincipalID: principal.ID,
		TokenHash:   hash[:],
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		log.Printf("login: session creation failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed creating session")
		return
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

	// principal.LastLoginAt still holds the pre-login value here - the
	// update below overwrites the row, but the response is built from what
	// was already read, so "last login" in the response means "before this
	// one" rather than "just now" (see GetUserPrincipalByName's comment).
	if _, err := s.queries.TouchPrincipalLastLogin(r.Context(), principal.ID); err != nil {
		log.Printf("login: last-login update failed: %v", err)
		// Not fatal to the login itself - a missed timestamp update is not
		// worth failing an otherwise-successful login over.
	}

	perms, err := s.resolvePermissions(r.Context(), principal.ID)
	if err != nil {
		log.Printf("login: permission resolution failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed resolving principal's permissions")
		return
	}
	resp, err := s.toWhoamiResponse(r.Context(), store.Principal{ID: principal.ID, Kind: principal.Kind, Name: principal.Name, LastLoginAt: principal.LastLoginAt}, perms)
	if err != nil {
		log.Printf("login: role resolution failed: %v", err)
		writeProblem(w, http.StatusInternalServerError, "failed resolving principal's role")
		return
	}
	writeJSON(w, http.StatusOK, resp)
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

func generateSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
