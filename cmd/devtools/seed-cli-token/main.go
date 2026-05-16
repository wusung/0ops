// Package main is a dev-only helper that mints a cli_token row directly in
// the local Postgres and prints the resulting bearer token.
//
// Use this when GitHub OAuth Device Flow is impractical (no browser, CI,
// scripted demos). The minted token is a real 'pat' kind row with the scope
// set you pass in, so it goes through the same middleware path as a normal
// PAT.
//
// Usage:
//
//	go run ./cmd/devtools/seed-cli-token \
//	    --database-url=postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable \
//	    --team-slug=acme-prod \
//	    --github-login=foxdie
//
// Or:  make seed-cli-token TEAM=acme-prod LOGIN=foxdie
//
// Requires `compose.override.yaml` to expose db on the host (the bundled
// `compose.override.yaml.example` shows the `15432:5432` mapping the
// Makefile target defaults to).
//
// Defaults:
//
//	--database-url   $DATABASE_URL, then postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable
//	--name           dev-seed
//	--ttl            720h (30 day)
//	--scopes         apps:read,apps:write,apps:delete,teams:read,members:manage,audit:read,incidents:read,incidents:write
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	sharedtoken "github.com/winshare/zeroops/internal/shared/token"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultDSN := os.Getenv("DATABASE_URL")
	if defaultDSN == "" {
		defaultDSN = "postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable"
	}

	dsn := flag.String("database-url", defaultDSN, "Postgres DSN (defaults to $DATABASE_URL)")
	teamSlug := flag.String("team-slug", "", "team slug (required)")
	login := flag.String("github-login", "", "github_login of the owner user (required)")
	name := flag.String("name", "dev-seed", "token name (free text)")
	ttl := flag.Duration("ttl", 720*time.Hour, "token TTL")
	scopesCSV := flag.String("scopes", "apps:read,apps:write,apps:delete,teams:read,members:manage,audit:read,incidents:read,incidents:write", "comma-separated scopes")
	flag.Parse()

	if strings.TrimSpace(*teamSlug) == "" || strings.TrimSpace(*login) == "" {
		flag.Usage()
		return errors.New("--team-slug and --github-login are required")
	}

	scopes := splitCSV(*scopesCSV)
	if len(scopes) == 0 {
		return errors.New("--scopes resolved to empty list")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var userID string
	if err := conn.QueryRow(ctx, `SELECT id FROM user_account WHERE github_login = $1`, *login).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no user_account with github_login=%q (run admin bootstrap-owner first)", *login)
		}
		return fmt.Errorf("lookup user: %w", err)
	}

	var teamID string
	if err := conn.QueryRow(ctx, `SELECT id FROM team WHERE slug = $1`, *teamSlug).Scan(&teamID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no team with slug=%q", *teamSlug)
		}
		return fmt.Errorf("lookup team: %w", err)
	}

	secret, err := sharedtoken.NewBearerTokenSecret()
	if err != nil {
		return fmt.Errorf("generate secret: %w", err)
	}
	hash, err := sharedtoken.HashBearerToken(secret)
	if err != nil {
		return fmt.Errorf("hash secret: %w", err)
	}

	expiresAt := time.Now().UTC().Add(*ttl)

	var tokenID string
	if err := conn.QueryRow(ctx, `
INSERT INTO cli_token (owner_user_id, team_id, kind, token_hash, name, scopes, expires_at)
VALUES ($1, $2, 'pat', $3, $4, $5, $6)
RETURNING id
`, userID, teamID, hash, *name, scopes, expiresAt).Scan(&tokenID); err != nil {
		return fmt.Errorf("insert cli_token: %w", err)
	}

	bearer := sharedtoken.FormatBearerToken("pat", tokenID, secret)
	fmt.Println(bearer)
	return nil
}

func splitCSV(in string) []string {
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
