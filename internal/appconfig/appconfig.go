// Package appconfig loads the small subset of settings the web UI's
// Configuration page can edit at runtime: DATABASE_URL and PODMAN_SOCKET.
// Everything else (RUN_LOG_DIR, GIT_REPO_DIR, etc.) stays plain
// os.Getenv-only, unchanged - this package is deliberately narrow.
//
// DATABASE_URL can't be stored inside the database it connects to (a
// bootstrap problem), so this repo's "config is environment only"
// convention (CLAUDE.md) is kept for everything except these two fields,
// which get one clearly-scoped exception: a dedicated KEY=value file,
// loaded by both cmd/api and cmd/supervisor at startup, with an actual
// environment variable of the same name always winning if set (so an
// ops/systemd EnvironmentFile= override still works unchanged). Neither
// process hot-reloads this file - a change only takes effect on the next
// restart of both.
package appconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the file-backed defaults for DATABASE_URL/PODMAN_SOCKET.
type Config struct {
	DatabaseURL  string
	PodmanSocket string
}

// DefaultPath returns $DESCENDENCE_CONFIG_FILE if set, else
// $HOME/.config/descendence/config.env - the same .config/descendence/
// family as DESCENDENCE_SCHEDULER_ENV_FILE's default (.env.sample).
func DefaultPath() (string, error) {
	if path := os.Getenv("DESCENDENCE_CONFIG_FILE"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving default config path: %w", err)
	}
	return filepath.Join(home, ".config", "descendence", "config.env"), nil
}

// Load reads path as KEY=value lines ('#' comments and blank lines
// skipped - the same shape as .env.sample, parsed by hand, no new
// dependency). A missing file is not an error: it returns a zero Config,
// since the file is optional and env vars/defaults may cover everything
// (e.g. before any PUT /api/v1/config has ever happened).
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "DATABASE_URL":
			cfg.DatabaseURL = strings.TrimSpace(value)
		case "PODMAN_SOCKET":
			cfg.PodmanSocket = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return cfg, nil
}

// Resolve applies the layering rule: an actual environment variable named
// envVar wins if set, otherwise fileValue is used. Kept as a small
// package-level helper, not a method, so call sites in cmd/api/main.go and
// cmd/supervisor/main.go read as a drop-in replacement for a bare
// os.Getenv call.
func Resolve(envVar, fileValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fileValue
}

// Save writes cfg to path as KEY=value lines, creating parent directories
// as needed. Permissions are 0600 - the file contains a database password.
// Always a full-file overwrite: both fields are known on every call site
// (PUT /api/v1/config always supplies both), so there is no partial-update
// case to preserve.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	contents := fmt.Sprintf("DATABASE_URL=%s\nPODMAN_SOCKET=%s\n", cfg.DatabaseURL, cfg.PodmanSocket)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
