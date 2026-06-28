package notify

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// RegisterTeamRoutes mounts the team-scoped webhook subscription endpoints onto
// a chi.Router that already carries Bearer → ResolveTeam → CheckMembership.
// Writes require admin + webhook:write; reads require admin + webhook:read
// (spec § 10, hard rule #9). member / viewer are rejected with forbidden_role.
func RegisterTeamRoutes(sr chi.Router, mw *auth.Middleware, svc *Service) {
	write := func(h http.HandlerFunc) http.Handler {
		return mw.CheckTokenScope(rbac.ActionManageWebhook, h)
	}
	read := func(h http.HandlerFunc) http.Handler {
		return mw.CheckTokenScope(rbac.ActionReadWebhook, h)
	}

	sr.Method(http.MethodPost, "/webhooks/subscriptions:preview", write(svc.PreviewCreateSubscription))
	sr.Method(http.MethodPost, "/webhooks/subscriptions", write(svc.ConfirmCreateSubscription))
	sr.Method(http.MethodPost, "/webhooks/subscriptions:confirm", write(svc.ConfirmWrite))
	sr.Method(http.MethodGet, "/webhooks/subscriptions", read(svc.ListSubscriptions))
	sr.Method(http.MethodGet, "/webhooks/subscriptions/{subscription_id}", read(svc.GetSubscription))
	sr.Method(http.MethodPost, "/webhooks/subscriptions/{subscription_id}:preview-rotate-secret", write(svc.PreviewRotateSecret))
	sr.Method(http.MethodPost, "/webhooks/subscriptions/{subscription_id}:preview-delete", write(svc.PreviewDeleteSubscription))
	sr.Method(http.MethodGet, "/webhooks/deliveries", read(svc.ListDeliveriesHandler))
	sr.Method(http.MethodPost, "/webhooks/deliveries/{delivery_id}:redeliver", write(svc.RedeliverHandler))
}
