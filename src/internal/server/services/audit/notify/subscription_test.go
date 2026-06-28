package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func newSubService(t *testing.T, pool *pgxpool.Pool) *Service {
	t.Helper()
	repo := dbpkg.NewRepository(pool)
	auditSvc := audit.NewService(repo, repo, nil)
	return NewService(pool, repo, auditSvc, staticResolver("93.184.216.34"), nil)
}

func TestPreviewCreateRejectsSSRFAndBadEvents(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "su")
	teamID, _ := seedTeam(ctx, t, pool, "subteam", "SubTeam")
	svc := newSubService(t, pool)

	// http:// → SSRF/scheme reject (maps to 422 webhook_url_invalid).
	_, err := svc.PreviewCreate(ctx, teamID, userID, dto.CreateWebhookSubscriptionRequest{
		URL: "http://hooks.example.com/x", Events: []string{"app.deleted"},
	})
	if !errors.Is(err, ErrInvalidWebhookURL) {
		t.Fatalf("http url err = %v, want ErrInvalidWebhookURL", err)
	}

	// unknown event key → ErrInvalidEvents.
	_, err = svc.PreviewCreate(ctx, teamID, userID, dto.CreateWebhookSubscriptionRequest{
		URL: "https://hooks.example.com/x", Events: []string{"not.an.event"},
	})
	if !errors.Is(err, ErrInvalidEvents) {
		t.Fatalf("bad events err = %v, want ErrInvalidEvents", err)
	}
}

func TestCreateConfirmRevealsSecretOnceAndListHidesIt(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "owner")
	teamID, _ := seedTeam(ctx, t, pool, "revealteam", "Reveal")
	svc := newSubService(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_subscription WHERE team_id = $1`, teamID)
	})

	prev, err := svc.PreviewCreate(ctx, teamID, userID, dto.CreateWebhookSubscriptionRequest{
		URL: "https://hooks.example.com/x", Events: []string{"app.deleted", "token.revoked"},
	})
	if err != nil {
		t.Fatalf("PreviewCreate: %v", err)
	}

	resp, err := svc.Confirm(ctx, teamID, userID, prev.PreviewID, "trace-1")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if resp.Secret == "" {
		t.Fatal("create confirm must reveal the signing key once")
	}
	if resp.ID == "" || len(resp.Events) != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// List never includes the secret (write-only reveal).
	list, err := svc.List(ctx, teamID)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = (%d items, %v)", len(list), err)
	}
	if list[0].Secret != "" {
		t.Error("List must not expose the signing key")
	}

	// Idempotent replay returns the same subscription WITHOUT re-revealing.
	replay, err := svc.Confirm(ctx, teamID, userID, prev.PreviewID, "trace-2")
	if err != nil {
		t.Fatalf("replay Confirm: %v", err)
	}
	if replay.Secret != "" {
		t.Error("idempotent replay must not re-reveal the secret")
	}
	if replay.ID != resp.ID {
		t.Error("replay should return the same subscription id")
	}
}

func TestRedeliverClonesDeliveryAndAudits(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "owner")
	teamID, _ := seedTeam(ctx, t, pool, "reteam", "Re")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})
	svc := newSubService(t, pool)

	origID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	// Mark the original as dropped so we redeliver a terminal delivery.
	if _, err := pool.Exec(ctx, `UPDATE webhook_delivery SET status='dropped' WHERE id=$1`, origID); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.Redeliver(ctx, teamID, userID, origID)
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if resp.DeliveryID == "" || resp.DeliveryID == origID || resp.Status != "pending" {
		t.Fatalf("redeliver resp = %+v (want new id, pending)", resp)
	}

	var auditN int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE team_id=$1 AND action=$2`, teamID, ActionRedeliver).Scan(&auditN); err != nil {
		t.Fatal(err)
	}
	if auditN != 1 {
		t.Fatalf("webhook_redeliver audit rows = %d, want 1", auditN)
	}
}
