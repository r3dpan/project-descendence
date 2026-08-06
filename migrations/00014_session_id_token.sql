-- +goose Up

-- Found live during Phase 9's guided interactive test: RP-Initiated Logout
-- against Authentik works (it accepts post_logout_redirect_uri and does
-- terminate the SSO session), but without id_token_hint identifying which
-- session is being ended, Authentik shows its own "are you sure" logout
-- confirmation page instead of ending the session directly - a linger step
-- nobody wants for what should be a one-click sign-out. The OIDC spec's
-- RP-Initiated Logout flow expects the RP to hand back the id_token it was
-- given at login for exactly this reason, so it has to be kept somewhere
-- between login and logout - the sessions row it already belongs to.
ALTER TABLE sessions ADD COLUMN id_token text;

-- +goose Down

ALTER TABLE sessions DROP COLUMN id_token;
