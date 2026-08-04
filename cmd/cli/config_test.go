package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig puts a config file in a temp dir and points the CLI at it.
// t.Setenv also clears the two value env vars, so each test starts from a
// known state rather than inheriting the developer's own shell.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envConfig, path)
	t.Setenv(envURL, "")
	t.Setenv(envToken, "")

	return path
}

func TestConfigFromFile(t *testing.T) {
	writeConfig(t, "url = http://example.test:8080\ntoken = sra_live_abc\n")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.baseURL != "http://example.test:8080" {
		t.Errorf("url = %q", cfg.baseURL)
	}
	if cfg.token != "sra_live_abc" {
		t.Errorf("token = %q", cfg.token)
	}
	if cfg.urlSource != sourceFile || cfg.tokenSource != sourceFile {
		t.Errorf("sources = %q/%q, want both %q", cfg.urlSource, cfg.tokenSource, sourceFile)
	}
}

// Environment beats file, per value - so a one-off override of just the URL
// still uses the stored token.
func TestConfigEnvOverridesFilePerValue(t *testing.T) {
	writeConfig(t, "url = http://from-file.test\ntoken = token-from-file\n")
	t.Setenv(envURL, "http://from-env.test")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.baseURL != "http://from-env.test" {
		t.Errorf("url = %q, want the environment to win", cfg.baseURL)
	}
	if cfg.urlSource != sourceEnv {
		t.Errorf("url source = %q, want %q", cfg.urlSource, sourceEnv)
	}
	if cfg.token != "token-from-file" {
		t.Errorf("token = %q, want the file value to survive", cfg.token)
	}
	if cfg.tokenSource != sourceFile {
		t.Errorf("token source = %q, want %q", cfg.tokenSource, sourceFile)
	}
}

// A missing config file is the normal case for someone using only env
// vars, so it must not be an error.
func TestConfigMissingFileIsFine(t *testing.T) {
	t.Setenv(envConfig, filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv(envURL, "http://from-env.test")
	t.Setenv(envToken, "token-from-env")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with no config file: %v", err)
	}
	if cfg.urlSource != sourceEnv || cfg.tokenSource != sourceEnv {
		t.Errorf("sources = %q/%q, want both %q", cfg.urlSource, cfg.tokenSource, sourceEnv)
	}
}

// The error has to name both ways of supplying each value, since the user
// could reasonably have meant either.
func TestConfigMissingValuesNameBothSources(t *testing.T) {
	path := writeConfig(t, "# nothing set here\n")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected an error with neither value set")
	}
	for _, want := range []string{envURL, envToken, keyURL, keyToken, path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An unreadable or malformed config file must fail loudly. Silently
// ignoring it would leave the user staring at a "not configured" error with
// a perfectly good file sitting right there.
func TestConfigMalformedFileIsAnError(t *testing.T) {
	writeConfig(t, "url = http://example.test\nthis line has no equals sign\n")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected an error for a malformed config file")
	}
}

func TestParseConfig(t *testing.T) {
	const contents = `
# a comment
; another comment

url   =   http://example.test:8080
TOKEN = "sra_live_quoted"
`

	values, err := parseConfig(strings.NewReader(contents))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := values[keyURL]; got != "http://example.test:8080" {
		t.Errorf("url = %q", got)
	}
	// Keys are case-insensitive and values lose one layer of quotes.
	if got := values[keyToken]; got != "sra_live_quoted" {
		t.Errorf("token = %q", got)
	}
}

// A typo'd key that quietly does nothing is exactly the sort of thing that
// costs an hour, so it is an error.
func TestParseConfigRejectsUnknownKeys(t *testing.T) {
	_, err := parseConfig(strings.NewReader("tokn = oops\n"))
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "tokn") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestParseConfigReportsLineNumbers(t *testing.T) {
	_, err := parseConfig(strings.NewReader("url = ok\n\n# comment\nbroken line\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %q does not point at line 4", err)
	}
}

// The hint must never be enough to reconstruct the token.
func TestTokenHint(t *testing.T) {
	const token = "sra_live_0123456789abcdef"

	hint := tokenHint(token)
	if strings.Contains(hint, "0123456789") {
		t.Errorf("hint %q leaks too much of the token", hint)
	}
	if !strings.HasSuffix(hint, "89abcdef") {
		t.Errorf("hint %q should end with the token's last 8 characters", hint)
	}
	if got := tokenHint("short"); got != "*****" {
		t.Errorf("a short token hinted as %q, want it fully masked", got)
	}
}
