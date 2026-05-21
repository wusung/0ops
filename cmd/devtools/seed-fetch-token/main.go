// Package main is a dev-only helper that mints a short-lived HS256 JWT for
// the GET /v1/uploads/{id}/archive endpoint (scope=download-upload).
//
// Used by tasks/m6-source-upload-e2e.sh (T22) to validate the archive
// download path without an actual GHA runner. Never included in production
// binaries.
//
// Usage:
//
//	OPS_BUILD_TOKEN_SECRET=dev-build-token-secret-change-me \
//	  go run ./cmd/devtools/seed-fetch-token \
//	    --team-id=<uuid> \
//	    --upload-id=upl_... \
//	    --deploy-run-id=<uuid> \
//	    --ttl=5m
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
)

func main() {
	teamID := flag.String("team-id", "", "team UUID")
	uploadID := flag.String("upload-id", "", "upload id (upl_...)")
	deployRunID := flag.String("deploy-run-id", "", "deploy run id")
	ttl := flag.Duration("ttl", 15*time.Minute, "token TTL")
	flag.Parse()

	if *teamID == "" || *uploadID == "" || *deployRunID == "" {
		fmt.Fprintln(os.Stderr, "error: --team-id, --upload-id, and --deploy-run-id are all required")
		flag.Usage()
		os.Exit(1)
	}

	secret := os.Getenv("OPS_BUILD_TOKEN_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "error: OPS_BUILD_TOKEN_SECRET is required")
		os.Exit(1)
	}

	signer := &ingestion.TokenSigner{
		Secret: []byte(secret),
		TTL:    *ttl,
	}
	tok, err := signer.Sign(ingestion.TokenClaims{
		TeamID:      *teamID,
		UploadID:    *uploadID,
		DeployRunID: *deployRunID,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
