package gitops

import (
	"fmt"
	"strings"
)

// imageRepoBase returns the GHCR repository for a team/app image, without a
// tag or digest. Single source of truth shared by the tag and digest forms so
// the two never drift.
func imageRepoBase(team, app string) string {
	return fmt.Sprintf("ghcr.io/winshare/0ops-apps/%s/%s", team, app)
}

// DigestPinnedImageRef builds an immutable digest-pinned image reference for a
// team/app image (supply-chain-security spec § 4.4, ADR-0017 § 3.4). The
// digest may be supplied with or without the "sha256:" prefix; the result is
// always "<repo>@sha256:<hex>".
//
// It returns ("", false) when digest is not a well-formed sha256 digest.
// Callers MUST NOT fall back to a mutable tag for the SC3-critical path when
// a digest was expected (spec hard rule #6): a tag can be re-pointed at a
// different digest, reopening the substitution window the digest pin closes.
func DigestPinnedImageRef(team, app, digest string) (string, bool) {
	hex, ok := normalizeSHA256(digest)
	if !ok {
		return "", false
	}
	return imageRepoBase(team, app) + "@sha256:" + hex, true
}

// normalizeSHA256 strips an optional "sha256:" prefix and validates the
// remainder is exactly 64 lowercase hex characters.
func normalizeSHA256(digest string) (string, bool) {
	d := strings.TrimSpace(digest)
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) != 64 {
		return "", false
	}
	for _, c := range d {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return "", false
		}
	}
	return d, true
}
