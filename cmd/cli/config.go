package main

import (
	"fmt"
	"os"
	"strings"
)

// Environment variables the CLI reads. Task 1.21 adds a config file behind
// these; until then they are the only source.
const (
	envURL   = "DESCENDENCE_URL"
	envToken = "DESCENDENCE_TOKEN"
)

type config struct {
	baseURL string
	token   string
}

// loadConfig resolves the server URL and token. Both are required - a CLI
// that silently defaults to some localhost guess would be worse than one
// that says exactly what is missing.
func loadConfig() (config, error) {
	cfg := config{
		baseURL: strings.TrimSpace(os.Getenv(envURL)),
		token:   strings.TrimSpace(os.Getenv(envToken)),
	}

	var missing []string
	if cfg.baseURL == "" {
		missing = append(missing, envURL)
	}
	if cfg.token == "" {
		missing = append(missing, envToken)
	}
	switch len(missing) {
	case 0:
	case 1:
		return config{}, fmt.Errorf("%s is not set", missing[0])
	default:
		return config{}, fmt.Errorf("%s are not set", strings.Join(missing, " and "))
	}

	return cfg, nil
}
