package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables the CLI reads. They take precedence over the config
// file, so a one-off `DESCENDENCE_URL=... descendence ...` overrides a
// stored default without editing anything.
const (
	envURL    = "DESCENDENCE_URL"
	envToken  = "DESCENDENCE_TOKEN"
	envConfig = "DESCENDENCE_CONFIG"
)

// Keys recognised in the config file.
const (
	keyURL   = "url"
	keyToken = "token"
)

// source records where a resolved value came from, so `descendence config`
// can explain itself when precedence surprises someone.
type source string

const (
	sourceEnv  source = "env"
	sourceFile source = "config file"
	sourceNone source = "unset"
)

type config struct {
	baseURL string
	token   string

	urlSource   source
	tokenSource source

	// path is the config file that was consulted, whether or not it
	// existed - worth showing either way when something is missing.
	path string
}

// loadConfig resolves the server URL and token from the config file, then
// the environment on top. Both are required: a CLI that quietly defaults to
// some localhost guess would be worse than one that says what is missing.
func loadConfig() (config, error) {
	path, err := configPath()
	if err != nil {
		return config{}, err
	}

	cfg := config{path: path, urlSource: sourceNone, tokenSource: sourceNone}

	values, err := readConfigFile(path)
	if err != nil {
		return config{}, err
	}
	if v := values[keyURL]; v != "" {
		cfg.baseURL, cfg.urlSource = v, sourceFile
	}
	if v := values[keyToken]; v != "" {
		cfg.token, cfg.tokenSource = v, sourceFile
	}

	if v := strings.TrimSpace(os.Getenv(envURL)); v != "" {
		cfg.baseURL, cfg.urlSource = v, sourceEnv
	}
	if v := strings.TrimSpace(os.Getenv(envToken)); v != "" {
		cfg.token, cfg.tokenSource = v, sourceEnv
	}

	// Name both ways of supplying a missing value - whichever the user
	// meant to use, the message points at it.
	var missing []string
	if cfg.baseURL == "" {
		missing = append(missing, fmt.Sprintf("%s (or %s in %s)", envURL, keyURL, path))
	}
	if cfg.token == "" {
		missing = append(missing, fmt.Sprintf("%s (or %s in %s)", envToken, keyToken, path))
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("not configured: set %s", strings.Join(missing, ", and "))
	}

	return cfg, nil
}

// configPath is $DESCENDENCE_CONFIG if set, else
// $XDG_CONFIG_HOME/descendence/config, else ~/.config/descendence/config.
func configPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(envConfig)); override != "" {
		return override, nil
	}

	// os.UserConfigDir already implements the XDG_CONFIG_HOME fallback.
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate a config directory: %w", err)
	}

	return filepath.Join(dir, "descendence", "config"), nil
}

// readConfigFile parses path, returning an empty map if it doesn't exist -
// a missing config file is the normal case for someone using env vars, not
// an error. A file that exists but can't be read or parsed *is* an error;
// silently ignoring it would be worse than failing.
func readConfigFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer file.Close()

	warnIfWorldReadable(file, path)

	values, err := parseConfig(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return values, nil
}

// warnIfWorldReadable complains about a token file anyone on the machine
// can read. It warns rather than refuses: unlike ssh, this CLI has no way
// to fall back to asking, and failing outright would be a worse trade for a
// homelab tool. (ARCHITECTURE.md §4.10 - the plaintext token is shown once
// and stored only by its holder.)
func warnIfWorldReadable(file *os.File, path string) {
	info, err := file.Stat()
	if err != nil {
		return
	}

	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		fmt.Fprintln(os.Stderr, styleHint.Render(fmt.Sprintf(
			"warning: %s is mode %#o and holds a token; chmod 600 it", path, mode)))
	}
}

// parseConfig reads "key = value" lines. Blank lines and lines starting
// with # or ; are ignored; values may be wrapped in matching quotes so a
// value with trailing spaces survives. Unknown keys are an error rather
// than a silent no-op - a typo'd "tokn" that quietly does nothing is
// exactly the sort of thing that costs an hour.
func parseConfig(r io.Reader) (map[string]string, error) {
	values := map[string]string{}

	scanner := bufio.NewScanner(r)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: expected key = value, got %q", lineNo, line)
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if key != keyURL && key != keyToken {
			return nil, fmt.Errorf("line %d: unknown key %q (want %s or %s)", lineNo, key, keyURL, keyToken)
		}

		values[key] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

// unquote strips one layer of matching quotes, so a value can keep
// significant whitespace if it ever needs to.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// tokenHint is the last few characters of a token - enough to tell two
// tokens apart without printing either. Matches the token_hint the server
// stores (ARCHITECTURE.md §4.10).
func tokenHint(token string) string {
	const hintLen = 8
	if len(token) <= hintLen {
		return strings.Repeat("*", len(token))
	}
	return "…" + token[len(token)-hintLen:]
}
