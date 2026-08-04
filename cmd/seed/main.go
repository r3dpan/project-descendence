package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r3dpan/project-descendence/internal/store"
)

// One-shot bootstrap token, per PLAN.md task 1.5. Run once against a fresh
// database; the token is shown only here and only once.
func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed creating database pool: %v", err)
	}
	defer pool.Close()

	token, err := generateToken()
	if err != nil {
		log.Fatalf("Failed generating token: %v", err)
	}
	hash := sha256.Sum256([]byte(token))

	queries := store.New(pool)
	principal, err := queries.CreateTokenPrincipal(ctx, store.CreateTokenPrincipalParams{
		Name:      "bootstrap",
		TokenHash: hash[:],
		TokenHint: pgtype.Text{String: token[len(token)-8:], Valid: true},
		Scopes:    []string{"read", "run", "admin"},
	})
	if err != nil {
		log.Fatalf("Failed creating bootstrap principal: %v", err)
	}

	fmt.Printf("Bootstrap principal #%d created (scopes: %v).\n", principal.ID, principal.Scopes)
	fmt.Printf("Token (shown once - store it now):\n\n  %s\n\n", token)
}

// sra_live_<64 hex chars>, per ARCHITECTURE.md §4.10.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sra_live_" + hex.EncodeToString(buf), nil
}
