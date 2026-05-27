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

	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: expiredID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "x", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (expired): %v", err)
	}
	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: freshID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "y", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (fresh): %v", err)
	}

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

func TestUploadRepository_ListExpiredUploads_ExcludesGCd(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "lxg-team", "LXG Team")
	userID := seedUser(ctx, t, pool, "lxg-user")

	activeExpiredID := uniqueUploadID(t)
	gcdExpiredID := uniqueUploadID(t)

	// Active expired row — should appear in result
	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: activeExpiredID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "a", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (active expired): %v", err)
	}
	// Already gc'd row — must be excluded by the WHERE status IN ('received','pinned') filter
	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: gcdExpiredID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1, SHA256: "g", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (will-gc): %v", err)
	}
	if err := repo.MarkUploadGCd(ctx, gcdExpiredID); err != nil {
		t.Fatalf("MarkUploadGCd: %v", err)
	}

	got, err := repo.ListExpiredUploads(ctx, 100)
	if err != nil {
		t.Fatalf("ListExpiredUploads: %v", err)
	}

	found := map[string]bool{}
	for _, u := range got {
		found[u.ID] = true
	}
	if !found[activeExpiredID] {
		t.Fatalf("active expired upload %s should be returned", activeExpiredID)
	}
	if found[gcdExpiredID] {
		t.Fatalf("gc'd upload %s should NOT be returned (status filter)", gcdExpiredID)
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

func TestUploadRepository_SumInertBytesByTeam(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "sib-team", "SIB Team")
	userID := seedUser(ctx, t, pool, "sib-user")

	// Insert: received (counts), pinned (counts), gc'd (excluded)
	receivedID := uniqueUploadID(t)
	pinnedID := uniqueUploadID(t)
	gcdID := uniqueUploadID(t)

	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: receivedID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 1000, SHA256: "r", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (received): %v", err)
	}
	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: pinnedID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 2000, SHA256: "p", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (will-pin): %v", err)
	}
	// Pin the second one so it transitions to 'pinned' status.
	if err := repo.PinUpload(ctx, teamID, pinnedID, time.Now().UTC().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("PinUpload: %v", err)
	}
	if err := repo.InsertUpload(ctx, dbpkg.Upload{
		ID: gcdID, TeamID: teamID, ActorUserID: userID,
		SizeBytes: 9999, SHA256: "g", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("InsertUpload (will-gc): %v", err)
	}
	if err := repo.MarkUploadGCd(ctx, gcdID); err != nil {
		t.Fatalf("MarkUploadGCd: %v", err)
	}

	total, err := repo.SumInertBytesByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("SumInertBytesByTeam: %v", err)
	}
	// Only received (1000) + pinned (2000) = 3000; gc'd (9999) excluded.
	if total != 3000 {
		t.Errorf("SumInertBytesByTeam = %d, want 3000", total)
	}
}

func TestUploadRepository_SumInertBytesByTeam_EmptyReturnsZero(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "sib0-team", "SIB0 Team")

	total, err := repo.SumInertBytesByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("SumInertBytesByTeam (empty): %v", err)
	}
	if total != 0 {
		t.Errorf("SumInertBytesByTeam (empty) = %d, want 0", total)
	}
}

func TestUploadRepository_CountPinnedByTeam(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "cpb-team", "CPB Team")
	userID := seedUser(ctx, t, pool, "cpb-user")

	pinnedA := uniqueUploadID(t)
	pinnedB := uniqueUploadID(t)
	receivedC := uniqueUploadID(t)

	for _, id := range []string{pinnedA, pinnedB, receivedC} {
		if err := repo.InsertUpload(ctx, dbpkg.Upload{
			ID: id, TeamID: teamID, ActorUserID: userID,
			SizeBytes: 1, SHA256: id, ArchiveFormat: "tar.zst",
			Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
		}); err != nil {
			t.Fatalf("InsertUpload %s: %v", id, err)
		}
	}
	// Pin A and B but not C.
	for _, id := range []string{pinnedA, pinnedB} {
		if err := repo.PinUpload(ctx, teamID, id, time.Now().UTC().Add(7*24*time.Hour)); err != nil {
			t.Fatalf("PinUpload %s: %v", id, err)
		}
	}

	count, err := repo.CountPinnedByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("CountPinnedByTeam: %v", err)
	}
	if count != 2 {
		t.Errorf("CountPinnedByTeam = %d, want 2", count)
	}
}

func TestUploadRepository_CountTeamUploadsSince(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ctus-team", "CTUS Team")
	userID := seedUser(ctx, t, pool, "ctus-user")

	// We can't directly control received_at because the DB sets it via DEFAULT NOW().
	// So we insert all rows now, then query with a "since" in the past to include all,
	// and also query with a "since" slightly in the future to exclude all.

	id1 := uniqueUploadID(t)
	id2 := uniqueUploadID(t)

	before := time.Now().UTC().Add(-time.Millisecond) // just before insertion
	for _, id := range []string{id1, id2} {
		if err := repo.InsertUpload(ctx, dbpkg.Upload{
			ID: id, TeamID: teamID, ActorUserID: userID,
			SizeBytes: 1, SHA256: id, ArchiveFormat: "tar.zst",
			Status: "received", ExpiresAt: time.Now().UTC().Add(time.Hour),
		}); err != nil {
			t.Fatalf("InsertUpload %s: %v", id, err)
		}
	}

	// Query "since before insertion" — should count both rows.
	count, err := repo.CountTeamUploadsSince(ctx, teamID, before)
	if err != nil {
		t.Fatalf("CountTeamUploadsSince: %v", err)
	}
	if count != 2 {
		t.Errorf("CountTeamUploadsSince (since before) = %d, want 2", count)
	}

	// Query "since far future" — should count 0.
	future := time.Now().UTC().Add(time.Hour)
	count2, err := repo.CountTeamUploadsSince(ctx, teamID, future)
	if err != nil {
		t.Fatalf("CountTeamUploadsSince (future): %v", err)
	}
	if count2 != 0 {
		t.Errorf("CountTeamUploadsSince (since future) = %d, want 0", count2)
	}
}
