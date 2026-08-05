package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/r3dpan/project-descendence/internal/store"
)

// One-shot token minting, per PLAN.md task 1.5 (the unflagged default:
// a "bootstrap" principal with every scope) and extended at task 5.3 for
// minting other token principals against an already-seeded database - most
// immediately, the scheduler principal a generated schedule's .service unit
// authenticates as when it calls POST /api/v1/schedules/{id}/trigger.
// principals.name is unique, so this is one-shot per name: running it twice
// with the same -name fails on the second insert, same as it always has for
// "bootstrap".
//
// -kind=user (task 7.3) mints a browser-login principal instead: password
// generated the same way the token is (crypto/rand, printed once), hashed
// with bcrypt before it ever reaches Postgres.
func main() {
	name := flag.String("name", "bootstrap", "principal name (must be unique)")
	scopes := flag.String("scopes", "read,run,admin", "comma-separated scopes")
	kind := flag.String("kind", "token", "principal kind: token or user")
	flag.Parse()

	if *kind != "token" && *kind != "user" {
		log.Fatalf("-kind must be \"token\" or \"user\", got %q", *kind)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	parsedScopes := strings.Split(*scopes, ",")
	for i, s := range parsedScopes {
		parsedScopes[i] = strings.TrimSpace(s)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed creating database pool: %v", err)
	}
	defer pool.Close()

	queries := store.New(pool)

	if *kind == "user" {
		seedUser(ctx, queries, *name, parsedScopes)
		return
	}

	token, err := generateToken()
	if err != nil {
		log.Fatalf("Failed generating token: %v", err)
	}
	hash := sha256.Sum256([]byte(token))

	principal, err := queries.CreateTokenPrincipal(ctx, store.CreateTokenPrincipalParams{
		Name:      *name,
		TokenHash: hash[:],
		TokenHint: pgtype.Text{String: token[len(token)-8:], Valid: true},
		Scopes:    parsedScopes,
	})
	if err != nil {
		log.Fatalf("Failed creating principal %q: %v", *name, err)
	}

	fmt.Printf("Principal #%d %q created (scopes: %v).\n", principal.ID, principal.Name, principal.Scopes)
	fmt.Printf("Token (shown once - store it now):\n\n  %s\n\n", token)
}

func seedUser(ctx context.Context, queries *store.Queries, name string, scopes []string) {
	password, err := generatePassword()
	if err != nil {
		log.Fatalf("Failed generating password: %v", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Failed hashing password: %v", err)
	}

	principal, err := queries.CreateUserPrincipal(ctx, store.CreateUserPrincipalParams{
		Name:         name,
		PasswordHash: passwordHash,
		Scopes:       scopes,
	})
	if err != nil {
		log.Fatalf("Failed creating principal %q: %v", name, err)
	}

	fmt.Printf("Principal #%d %q created (scopes: %v).\n", principal.ID, principal.Name, principal.Scopes)
	fmt.Printf("Password (shown once - store it now):\n\n  %s\n\n", password)
}

// sra_live_<64 hex chars>, per ARCHITECTURE.md §4.10.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sra_live_" + hex.EncodeToString(buf), nil
}

// 24 hex chars, deliberately short of bcrypt's 72-byte input limit (unlike
// generateToken's sra_live_-prefixed 73 bytes, which exceeds it) - this is
// typed into a login form, not pasted as a bearer token, so it stays short
// enough to read out.
func generatePassword() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
