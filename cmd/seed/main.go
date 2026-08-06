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

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/r3dpan/project-descendence/internal/store"
)

// One-shot token minting, per PLAN.md task 1.5 (the unflagged default:
// a "bootstrap" principal with the admin role) and extended at task 5.3 for
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
//
// Phase 8: cmd/seed remains the chicken-and-egg breaker for RBAC - creating
// a principal via the API requires the users:write permission, which the
// very first admin can't have yet. cmd/seed assigns a role by a direct DB
// write (assignRole below), bypassing RequirePermission by construction,
// the same way it already bypasses the API entirely.
func main() {
	name := flag.String("name", "bootstrap", "principal name (must be unique)")
	role := flag.String("role", "admin", "role name: admin, operator or viewer")
	kind := flag.String("kind", "token", "principal kind: token or user")
	flag.Parse()

	if *kind != "token" && *kind != "user" {
		log.Fatalf("-kind must be \"token\" or \"user\", got %q", *kind)
	}

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

	queries := store.New(pool)

	if *kind == "user" {
		seedUser(ctx, queries, *name, *role)
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
	})
	if err != nil {
		log.Fatalf("Failed creating principal %q: %v", *name, err)
	}
	assignRole(ctx, queries, principal.ID, *role)

	fmt.Printf("Principal #%d %q created (role: %s).\n", principal.ID, principal.Name, *role)
	fmt.Printf("Token (shown once - store it now):\n\n  %s\n\n", token)
}

func seedUser(ctx context.Context, queries *store.Queries, name, role string) {
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
	})
	if err != nil {
		log.Fatalf("Failed creating principal %q: %v", name, err)
	}
	assignRole(ctx, queries, principal.ID, role)

	fmt.Printf("Principal #%d %q created (role: %s).\n", principal.ID, principal.Name, role)
	fmt.Printf("Password (shown once - store it now):\n\n  %s\n\n", password)
}

// assignRole is the direct-DB-write half of cmd/seed's role assignment,
// shared by the token and user paths - both need the identical "look up the
// role by name, upsert principal_roles" sequence.
func assignRole(ctx context.Context, queries *store.Queries, principalID int64, roleName string) {
	roleRow, err := queries.GetRoleByName(ctx, roleName)
	if err != nil {
		log.Fatalf("Failed looking up role %q: %v", roleName, err)
	}
	if err := queries.SetPrincipalRole(ctx, store.SetPrincipalRoleParams{
		PrincipalID: principalID,
		RoleID:      roleRow.ID,
	}); err != nil {
		log.Fatalf("Failed assigning role %q to principal #%d: %v", roleName, principalID, err)
	}
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
