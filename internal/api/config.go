package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/r3dpan/project-descendence/internal/appconfig"
)

type configResponse struct {
	DatabaseURL  string `json:"databaseUrl"`
	PodmanSocket string `json:"podmanSocket"`
}

// maskDatabaseURL replaces a connection string's password with "***" for
// display. Falls back to returning s unmasked on parse failure - defensive
// only, since validateDatabaseURL below rejects an unparseable value before
// it can ever be saved through this handler.
//
// Splices the literal text "***" into the username@ boundary rather than
// going through url.UserPassword(...).String() - net/url percent-encodes
// userinfo characters outside a narrow unreserved set, so "***" round-trips
// through String() as "%2A%2A%2A", not the literal asterisks a human
// expects to see.
func maskDatabaseURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if _, hasPassword := u.User.Password(); !hasPassword {
		return s
	}
	username := u.User.Username()
	u.User = url.User(username)
	withoutPassword := u.String()
	return strings.Replace(withoutPassword, username+"@", username+":***@", 1)
}

func validateDatabaseURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("databaseUrl is not a valid URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("databaseUrl must use the postgres:// or postgresql:// scheme")
	}
	return nil
}

func validatePodmanSocketPath(s string) error {
	if s == "" {
		return fmt.Errorf("podmanSocket must not be empty")
	}
	return nil
}

// GetConfigHandler returns the DATABASE_URL/PODMAN_SOCKET currently
// persisted in the config file appconfigPath points at (the same file
// cmd/api and cmd/supervisor load at their own startup). The DB URL's
// password is masked - see PutConfigHandler for how a resubmitted mask is
// handled without clobbering the real password.
func (s *APIServer) GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	cfg, err := appconfig.Load(s.appconfigPath)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, configResponse{
		DatabaseURL:  maskDatabaseURL(cfg.DatabaseURL),
		PodmanSocket: cfg.PodmanSocket,
	})
}

// PutConfigHandler validates and persists new DATABASE_URL/PODMAN_SOCKET
// values. Only shape is validated (URL scheme, non-empty socket path) - not
// live reachability, since the process handling this request isn't
// necessarily running in the same environment/mount namespace the *next*
// boot will have, so a live check here would just report false confidence.
// Neither cmd/api nor cmd/supervisor hot-reloads this file: the caller must
// restart both for a saved change to take effect.
func (s *APIServer) PutConfigHandler(w http.ResponseWriter, r *http.Request) {
	var req configResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// GetConfigHandler above masks the stored password as "***" so the UI
	// never displays it in full. If the client resubmits that exact
	// placeholder unchanged (the common case: editing only podmanSocket),
	// splice the real password back in from what's already on disk rather
	// than persisting the literal string "***" as the new password.
	if u, err := url.Parse(req.DatabaseURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw == "***" {
			current, err := appconfig.Load(s.appconfigPath)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, err.Error())
				return
			}
			if cu, err := url.Parse(current.DatabaseURL); err == nil && cu.User != nil {
				if realPw, ok := cu.User.Password(); ok {
					u.User = url.UserPassword(u.User.Username(), realPw)
					req.DatabaseURL = u.String()
				}
			}
		}
	}

	if err := validateDatabaseURL(req.DatabaseURL); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := validatePodmanSocketPath(req.PodmanSocket); err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	if err := appconfig.Save(s.appconfigPath, appconfig.Config{
		DatabaseURL:  req.DatabaseURL,
		PodmanSocket: req.PodmanSocket,
	}); err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, configResponse{
		DatabaseURL:  maskDatabaseURL(req.DatabaseURL),
		PodmanSocket: req.PodmanSocket,
	})
}
