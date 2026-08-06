// Package oidc wires this process into a single external identity provider
// (Phase 9, ARCHITECTURE.md §4.10). It exists to hold exactly the parts that
// are genuinely provider-specific - discovery, the OAuth2 endpoint set, and
// ID token verification - so internal/api's login/callback handlers never
// import go-oidc/oauth2 directly. Group claims are never read here (that is
// what keeps this provider-agnostic, per PLAN.md's Phase 9 goal): roles stay
// entirely local, in principal_roles.
package oidc

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Options configures discovery. Every field is required except Scopes,
// which defaults to "openid profile email" - the minimum claim set the
// callback handler needs (sub for identity, preferred_username for the JIT
// display name and the bootstrap-admin match).
type Options struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// Config is built once at process startup (cmd/api's main, task 9.4) from
// Options and never rebuilt - there is one IdP per deployment, not one per
// request.
type Config struct {
	Provider *oidc.Provider
	Verifier *oidc.IDTokenVerifier
	OAuth2   oauth2.Config
}

// New performs discovery against IssuerURL. Discovery failure is fatal to
// the caller by design (PLAN.md task 9.4: "Discovery failure is fatal, not
// degraded") - a server that starts anyway would accept requests against a
// login path that can never work, which is worse than not starting.
func New(ctx context.Context, opts Options) (*Config, error) {
	if opts.IssuerURL == "" {
		return nil, fmt.Errorf("oidc: issuer URL is required")
	}
	if opts.ClientID == "" {
		return nil, fmt.Errorf("oidc: client ID is required")
	}
	if opts.RedirectURL == "" {
		return nil, fmt.Errorf("oidc: redirect URL is required")
	}

	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	provider, err := oidc.NewProvider(ctx, opts.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovering issuer %q: %w", opts.IssuerURL, err)
	}

	return &Config{
		Provider: provider,
		Verifier: provider.Verifier(&oidc.Config{ClientID: opts.ClientID}),
		OAuth2: oauth2.Config{
			ClientID:     opts.ClientID,
			ClientSecret: opts.ClientSecret,
			RedirectURL:  opts.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
	}, nil
}

// ParseScopes splits an env var's space-separated scope list the way
// OIDC_SCOPES is documented (default "openid profile email"). A blank
// string yields no scopes, letting New fall back to its own default rather
// than this function duplicating it.
func ParseScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Fields(raw)
}
