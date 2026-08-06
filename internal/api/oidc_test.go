package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	oidclib "github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	oidcpkg "github.com/r3dpan/project-descendence/internal/oidc"
	"github.com/r3dpan/project-descendence/internal/store"
)

func pgTextOf(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// fakeIDP is a minimal OIDC provider backed by an in-process httptest.Server
// - real discovery, real RS256-signed ID tokens, real signature
// verification through go-oidc's remote key set, without needing a live
// Authentik instance (task 9.12). Its /token handler ignores the
// authorization code entirely and just returns whatever ID token the test
// staged via nextIDToken - this harness is testing CallbackHandler's own
// state/nonce/principal-resolution logic, not the token exchange protocol
// itself.
type fakeIDP struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	kid         string
	clientID    string
	nextIDToken string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	f := &fakeIDP{key: key, kid: "test-key-1", clientID: "test-client"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.server.URL,
			"authorization_endpoint":                f.server.URL + "/authorize",
			"token_endpoint":                         f.server.URL + "/token",
			"jwks_uri":                               f.server.URL + "/jwks",
			"id_token_signing_alg_values_supported":  []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{
			Keys: []jose.JSONWebKey{
				{Key: &key.PublicKey, KeyID: f.kid, Algorithm: "RS256", Use: "sig"},
			},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"id_token":     f.nextIDToken,
		})
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// signIDToken builds a signed RS256 ID token, mirroring what a real IdP's
// /token endpoint returns after a successful exchange.
func (f *fakeIDP) signIDToken(t *testing.T, subject, nonce, preferredUsername string) string {
	t.Helper()

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", f.kid),
	)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	now := time.Now()
	claims := map[string]any{
		"iss":                f.server.URL,
		"sub":                subject,
		"aud":                f.clientID,
		"exp":                now.Add(5 * time.Minute).Unix(),
		"iat":                now.Unix(),
		"nonce":              nonce,
		"preferred_username": preferredUsername,
	}

	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("signing id_token: %v", err)
	}
	return token
}

// oidcTestFixture wires a fake IDP to a real APIServer backed by the real
// dev database - CallbackHandler's principal resolution (task 9.6) does
// real inserts/lookups, only the identity-provider side is faked.
type oidcTestFixture struct {
	server             *APIServer
	idp                *fakeIDP
	queries            *store.Queries
	pool               *pgxpool.Pool
	bootstrapUsername  string
}

func newOIDCTestFixture(t *testing.T, bootstrapUsername string) (*oidcTestFixture, context.Context) {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("cannot create a pool: %v", err)
	}
	t.Cleanup(pool.Close)

	queries := store.New(pool)
	if _, err := queries.Ping(ctx); err != nil {
		t.Skipf("database not reachable: %v", err)
	}

	idp := newFakeIDP(t)

	provider, err := oidclib.NewProvider(ctx, idp.server.URL)
	if err != nil {
		t.Fatalf("discovering fake IdP: %v", err)
	}
	oidcConfig := &oidcpkg.Config{
		Provider: provider,
		Verifier: provider.Verifier(&oidclib.Config{ClientID: idp.clientID}),
		OAuth2: oauth2.Config{
			ClientID:     idp.clientID,
			ClientSecret: "test-secret",
			RedirectURL:  "http://127.0.0.1:8080/api/v1/auth/callback",
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{"openid"},
		},
	}

	server := NewAPIServer("test", "test", "test", queries, nil, t.TempDir(), nil, nil, "", oidcConfig, bootstrapUsername)

	return &oidcTestFixture{server: server, idp: idp, queries: queries, pool: pool, bootstrapUsername: bootstrapUsername}, ctx
}

// cleanupPrincipal removes a principal (and, transitively via ON DELETE
// CASCADE, any session) created by a test - test principals must never leak
// between runs, matching CLAUDE.md's "every session starts on a clean
// state".
func (f *oidcTestFixture) cleanupPrincipal(name string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = f.pool.Exec(cleanupCtx, "DELETE FROM principals WHERE name = $1", name)
}

// callback issues a GET /api/v1/auth/callback with the given state/nonce/
// verifier cookies staged (as LoginHandler would have set them) and the
// given query state/code, then runs it through the real CallbackHandler.
func (f *oidcTestFixture) callback(t *testing.T, ctx context.Context, cookieState, cookieNonce, queryState, code string) *httptest.ResponseRecorder {
	t.Helper()

	url := fmt.Sprintf("/api/v1/auth/callback?state=%s&code=%s", queryState, code)
	r := httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: cookieState})
	r.AddCookie(&http.Cookie{Name: oauthNonceCookieName, Value: cookieNonce})
	r.AddCookie(&http.Cookie{Name: oauthVerifierCookieName, Value: "test-verifier"})

	w := httptest.NewRecorder()
	f.server.CallbackHandler(w, r)
	return w
}

func hasSessionCookie(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return true
		}
	}
	return false
}

func TestCallbackStateMismatch(t *testing.T) {
	fx, ctx := newOIDCTestFixture(t, "")

	w := fx.callback(t, ctx, "cookie-state", "cookie-nonce", "different-state", "some-code")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
	if hasSessionCookie(w) {
		t.Fatal("state mismatch must not mint a session")
	}
}

func TestCallbackNonceMismatch(t *testing.T) {
	fx, ctx := newOIDCTestFixture(t, "")
	subject := fmt.Sprintf("nonce-mismatch-%d", time.Now().UnixNano())
	name := "oidc-test-" + subject
	t.Cleanup(func() { fx.cleanupPrincipal(name) })

	fx.idp.nextIDToken = fx.idp.signIDToken(t, subject, "token-nonce", name)

	w := fx.callback(t, ctx, "state-1", "cookie-nonce-does-not-match", "state-1", "some-code")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
	if hasSessionCookie(w) {
		t.Fatal("nonce mismatch must not mint a session")
	}

	if _, err := fx.queries.GetUserPrincipalByOIDCSubject(ctx, store.GetUserPrincipalByOIDCSubjectParams{
		OidcIssuer:  pgTextOf(fx.idp.server.URL),
		OidcSubject: pgTextOf(subject),
	}); err == nil {
		t.Fatal("nonce mismatch must not JIT-provision a principal")
	} else if err != pgx.ErrNoRows {
		t.Fatalf("unexpected lookup error: %v", err)
	}
}

func TestCallbackJITProvisionsRolelessPrincipal(t *testing.T) {
	fx, ctx := newOIDCTestFixture(t, "")
	subject := fmt.Sprintf("jit-%d", time.Now().UnixNano())
	name := "oidc-test-" + subject
	t.Cleanup(func() { fx.cleanupPrincipal(name) })

	fx.idp.nextIDToken = fx.idp.signIDToken(t, subject, "matching-nonce", name)

	w := fx.callback(t, ctx, "state-1", "matching-nonce", "state-1", "some-code")

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	if !hasSessionCookie(w) {
		t.Fatal("expected a session cookie to be set")
	}

	row, err := fx.queries.GetUserPrincipalByOIDCSubject(ctx, store.GetUserPrincipalByOIDCSubjectParams{
		OidcIssuer:  pgTextOf(fx.idp.server.URL),
		OidcSubject: pgTextOf(subject),
	})
	if err != nil {
		t.Fatalf("expected a JIT-provisioned principal, lookup failed: %v", err)
	}
	if row.RevokedAt.Valid {
		t.Fatal("freshly JIT-provisioned principal must not be revoked")
	}

	if _, err := fx.queries.GetPrincipalRoleName(ctx, row.ID); err != pgx.ErrNoRows {
		t.Fatalf("expected no role assigned to a fresh JIT principal, got err=%v", err)
	}
}

func TestCallbackBootstrapUsernameAssignsAdmin(t *testing.T) {
	subject := fmt.Sprintf("bootstrap-%d", time.Now().UnixNano())
	name := "oidc-test-" + subject
	fx, ctx := newOIDCTestFixture(t, name)
	t.Cleanup(func() { fx.cleanupPrincipal(name) })

	fx.idp.nextIDToken = fx.idp.signIDToken(t, subject, "matching-nonce", name)

	w := fx.callback(t, ctx, "state-1", "matching-nonce", "state-1", "some-code")

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}

	row, err := fx.queries.GetUserPrincipalByOIDCSubject(ctx, store.GetUserPrincipalByOIDCSubjectParams{
		OidcIssuer:  pgTextOf(fx.idp.server.URL),
		OidcSubject: pgTextOf(subject),
	})
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}

	roleName, err := fx.queries.GetPrincipalRoleName(ctx, row.ID)
	if err != nil {
		t.Fatalf("expected the bootstrap match to have a role, got err=%v", err)
	}
	if roleName != "admin" {
		t.Fatalf("expected role %q, got %q", "admin", roleName)
	}
}

func TestCallbackRevokedSubjectRefused(t *testing.T) {
	fx, ctx := newOIDCTestFixture(t, "")
	subject := fmt.Sprintf("revoked-%d", time.Now().UnixNano())
	name := "oidc-test-" + subject
	t.Cleanup(func() { fx.cleanupPrincipal(name) })

	created, err := fx.queries.CreateUserPrincipalOIDC(ctx, store.CreateUserPrincipalOIDCParams{
		Name:        name,
		OidcIssuer:  pgTextOf(fx.idp.server.URL),
		OidcSubject: pgTextOf(subject),
	})
	if err != nil {
		t.Fatalf("seeding revoked principal: %v", err)
	}
	if _, err := fx.queries.RevokePrincipal(ctx, created.ID); err != nil {
		t.Fatalf("revoking principal: %v", err)
	}

	fx.idp.nextIDToken = fx.idp.signIDToken(t, subject, "matching-nonce", name)

	w := fx.callback(t, ctx, "state-1", "matching-nonce", "state-1", "some-code")

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if hasSessionCookie(w) {
		t.Fatal("a revoked subject must never be resurrected with a session")
	}
}
