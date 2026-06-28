package notify

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// NotifyEvent is the enqueue-time projection of an audit row, carrying only the
// whitelisted fields the outbound payload may expose. It is resolved inside the
// audit insert transaction (team slug + actor login are joined there). It
// deliberately omits args / result (hard rule #2).
type NotifyEvent struct {
	AuditLogID  int64
	TeamID      string // uuid
	TeamSlug    string
	Action      string
	Source      string
	SubjectType string
	SubjectID   *string
	ActorLogin  *string // github_login; nil for system-sourced events
	Outcome     string
	OccurredAt  time.Time
	TraceID     *string
}

// forbiddenPayloadSubstrings must never appear as a JSON object KEY in an
// outbound payload (spec § 6.2 / hard rule #2). The check is a defense-in-depth
// assertion; BuildPayload only ever sets whitelisted fields.
var forbiddenPayloadKeys = []string{"secret", "password", "token", "signature", "args", "result"}

// BuildPayload assembles the redacted outbound payload for an event. deliveryID
// is the webhook_delivery.id (also the X-0ops-Delivery header) so the receiver
// can dedup. The Summary comes from the catalog summariser (metadata-only).
func BuildPayload(deliveryID string, eventKey EventKey, summary string, ev NotifyEvent) dto.NotificationPayload {
	p := dto.NotificationPayload{
		DeliveryID:  deliveryID,
		Event:       string(eventKey),
		TeamSlug:    ev.TeamSlug,
		OccurredAt:  ev.OccurredAt.UTC(),
		Source:      ev.Source,
		SubjectType: ev.SubjectType,
		SubjectID:   ev.SubjectID,
		Outcome:     ev.Outcome,
		AuditID:     ev.AuditLogID,
		TraceID:     ev.TraceID,
		Summary:     summary,
	}
	if ev.ActorLogin != nil && strings.TrimSpace(*ev.ActorLogin) != "" {
		p.Actor = ev.ActorLogin
	}
	return p
}

// MarshalPayload serialises a payload to the exact bytes that are stored on
// webhook_delivery.payload and POSTed (and signed) verbatim on every retry. Go
// marshals struct fields in declaration order, so the bytes are stable across
// retries — a requirement for receiver-side dedup on a stable signature.
func MarshalPayload(p dto.NotificationPayload) ([]byte, error) {
	return json.Marshal(p)
}

// AssertNoForbiddenKeys returns false if the marshalled payload contains any
// forbidden top-level-or-nested JSON object key. Used by tests and as a runtime
// guard before persisting a delivery snapshot.
func AssertNoForbiddenKeys(body []byte) bool {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	return scanKeys(decoded)
}

func scanKeys(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lk := strings.ToLower(k)
			for _, bad := range forbiddenPayloadKeys {
				if strings.Contains(lk, bad) {
					return false
				}
			}
			if !scanKeys(val) {
				return false
			}
		}
	case []any:
		for _, val := range t {
			if !scanKeys(val) {
				return false
			}
		}
	}
	return true
}
