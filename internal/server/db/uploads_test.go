package db_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

func TestUploadRepository_InsertAndGet(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "tu-team", "TU Team")
	userID := seedUser(ctx, t, pool, "tu-user")

	now := time.Now().UTC()
	in := dbpkg.Upload{
		ID:            uniqueUploadID(t),
		TeamID:        teamID,
		ActorUserID:   userID,
		SizeBytes:     1024,
		SHA256:        "deadbeef",
		ArchiveFormat: "tar.zst",
		Status:        "received",
		ExpiresAt:     now.Add(24 * time.Hour),
	}
	if err := repo.InsertUpload(ctx, in); err != nil {
		t.Fatalf("InsertUpload: %v", err)
	}

	got, err := repo.GetUpload(ctx, teamID, in.ID)
	if err != nil {
		t.Fatalf("GetUpload: %v", err)
	}
	if got.SHA256 != "deadbeef" || got.SizeBytes != 1024 || got.Status != "received" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestUploadRepository_GetCrossTeamReturnsNotFound(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamA, _ := seedTeam(ctx, t, pool, "tcr-a", "Team A")
	teamB, _ := seedTeam(ctx, t, pool, "tcr-b", "Team B")
	userID := seedUser(ctx, t, pool, "tcr-user")

	in := dbpkg.Upload{
		ID:            uniqueUploadID(t),
		TeamID:        teamA,
		ActorUserID:   userID,
		SizeBytes:     1,
		SHA256:        "x",
		ArchiveFormat: "tar.zst",
		Status:        "received",
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}
	if err := repo.InsertUpload(ctx, in); err != nil {
		t.Fatalf("InsertUpload: %v", err)
	}

	_, err := repo.GetUpload(ctx, teamB, in.ID)
	if !errors.Is(err, dbpkg.ErrUploadNotFound) {
		t.Fatalf("expected ErrUploadNotFound, got %v", err)
	}
	// also ensure the underlying pgx not-found is not exposed bare
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("bare pgx.ErrNoRows leaked through ErrUploadNotFound; sentinel must not wrap pgx.ErrNoRows")
	}
}

func TestUploadRepository_PinUpload_FlipsStatusAndExtendsExpiry(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "pu-team", "PU Team")
	userID := seedUser(ctx, t, pool, "pu-user")

	in := dbpkg.Upload{
		ID:            uniqueUploadID(t),
		TeamID:        teamID,
		ActorUserID:   userID,
		SizeBytes:     1,
		SHA256:        "x",
		ArchiveFormat: "tar.zst",
		Status:        "received",
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}
	if err := repo.InsertUpload(ctx, in); err != nil {
		t.Fatalf("InsertUpload: %v", err)
	}

	newExpiry := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Microsecond)
	if err := repo.PinUpload(ctx, teamID, in.ID, newExpiry); err != nil {
		t.Fatalf("PinUpload: %v", err)
	}

	got, err := repo.GetUpload(ctx, teamID, in.ID)
	if err != nil {
		t.Fatalf("GetUpload after pin: %v", err)
	}
	if got.Status != "pinned" {
		t.Fatalf("status: want pinned, got %s", got.Status)
	}
	if got.PinnedAt == nil {
		t.Fatalf("expected pinned_at to be set")
	}
	// expires_at should match newExpiry (allowing for postgres microsecond precision)
	if !got.ExpiresAt.Round(time.Second).Equal(newExpiry.Round(time.Second)) {
		t.Fatalf("expires_at: want %v, got %v", newExpiry, got.ExpiresAt)
	}
}

func TestUploadRepository_PinUpload_RejectsNonReceivedStatus(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "pn-team", "PN Team")
	userID := seedUser(ctx, t, pool, "pn-user")

	in := dbpkg.Upload{
		ID:            uniqueUploadID(t),
		TeamID:        teamID,
		ActorUserID:   userID,
		SizeBytes:     1,
		SHA256:        "x",
		ArchiveFormat: "tar.zst",
		Status:        "pinned", // already pinned
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}
	if err := repo.InsertUpload(ctx, in); err != nil {
		t.Fatalf("InsertUpload: %v", err)
	}

	err := repo.PinUpload(ctx, teamID, in.ID, time.Now().UTC().Add(24*time.Hour))
	if !errors.Is(err, dbpkg.ErrUploadNotFound) {
		t.Fatalf("expected ErrUploadNotFound (no row in received status), got %v", err)
	}
}

func TestUploadRepository_ListExpiredUploads(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "lx-team", "LX Team")
	userID := seedUser(ctx, t, pool, "lx-user")

	expiredID := uniqueUploadID(t)
	freshID := uniqueUploadID(t)

	_ = repo.InsertUpload(ctx, dbpkg.Upload{
		ID: expiredID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "x", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-time.Hour),
	})
	_ = repo.InsertUpload(ctx, dbpkg.Upload{
		ID: freshID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "y", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
	})

	got, err := repo.ListExpiredUploads(ctx, 100)
	if err != nil {
		t.Fatalf("ListExpiredUploads: %v", err)
	}

	found := map[string]bool{}
	for _, u := range got {
		found[u.ID] = true
	}
	if !found[expiredID] {
		t.Fatalf("expired upload %s not returned", expiredID)
	}
	if found[freshID] {
		t.Fatalf("fresh upload %s should NOT be in expired list", freshID)
	}
}

func TestUploadRepository_MarkUploadGCd(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "gc-team", "GC Team")
	userID := seedUser(ctx, t, pool, "gc-user")

	in := dbpkg.Upload{
		ID: uniqueUploadID(t), TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "x", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := repo.InsertUpload(ctx, in); err != nil {
		t.Fatalf("InsertUpload: %v", err)
	}

	if err := repo.MarkUploadGCd(ctx, in.ID); err != nil {
		t.Fatalf("MarkUploadGCd: %v", err)
	}

	got, err := repo.GetUpload(ctx, teamID, in.ID)
	if err != nil {
		t.Fatalf("GetUpload: %v", err)
	}
	if got.Status != "gc'd" {
		t.Fatalf("status: want gc'd, got %s", got.Status)
	}
}

func uniqueUploadID(t *testing.T) string {
	t.Helper()
	return "upl_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")) + "_" + time.Now().Format("150405.000000000")
}
