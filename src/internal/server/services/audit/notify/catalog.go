package notify

import (
	"fmt"
	"sort"
)

// EventKey is the stable, outward-facing event name for a subscribable audit
// action (spec § 5.1). It is decoupled from the audit action string so the
// audit layer can rename actions without breaking the external contract.
type EventKey string

// catalogEntry maps an audit action to its event key and a summariser. The
// summariser composes a human-readable Summary from WHITELISTED metadata only
// — never from audit args / result (spec § 6.2, hard rule #2). v1 summaries are
// therefore metadata-shaped (actor + semantics); per-action enrichment from a
// redacted whitelist is a documented future item (spec § 16).
type catalogEntry struct {
	event     EventKey
	summarise func(ev NotifyEvent) string
}

// catalog is the audit action → event mapping (spec § 5.1). Actions absent
// here are not subscribable (high-frequency / low-signal events and the
// notify config events themselves are deliberately excluded, spec § 5.2).
var catalog = map[string]catalogEntry{
	"delete_app_confirm": {
		event:     "app.deleted",
		summarise: func(ev NotifyEvent) string { return phrase("App deleted", ev) },
	},
	"token_create": {
		event:     "token.created",
		summarise: func(ev NotifyEvent) string { return phrase("PAT created", ev) },
	},
	"token_revoke": {
		event:     "token.revoked",
		summarise: func(ev NotifyEvent) string { return phrase("PAT revoked", ev) },
	},
	"plan_change": {
		event:     "plan.changed",
		summarise: func(ev NotifyEvent) string { return phrase("Plan changed", ev) },
	},
	"abuse_detected": {
		event:     "abuse.detected",
		summarise: func(ev NotifyEvent) string { return "Abuse signal detected" },
	},
	"reconciler_failed_permanently": {
		event:     "reconciler.failed_permanently",
		summarise: func(ev NotifyEvent) string { return "Reconcile failed permanently" },
	},
	"secret_rotate_finalize": {
		event:     "secret.rotated",
		summarise: func(ev NotifyEvent) string { return phrase("Secret rotation finalized", ev) },
	},
	"member_invite": {
		event:     "member.added",
		summarise: func(ev NotifyEvent) string { return phrase("Member added", ev) },
	},
	"member_remove": {
		event:     "member.removed",
		summarise: func(ev NotifyEvent) string { return phrase("Member removed", ev) },
	},
	"member_role_change": {
		event:     "member.role_changed",
		summarise: func(ev NotifyEvent) string { return phrase("Member role changed", ev) },
	},
	"domain_grace_unbind": {
		event:     "domain.unbound",
		summarise: func(ev NotifyEvent) string { return "Custom domain unbound" },
	},
}

// phrase builds a metadata-only summary: "<base> by <actor>" when an actor
// login is present, otherwise "<base> (<source>)". It never reads args/result.
func phrase(base string, ev NotifyEvent) string {
	if ev.ActorLogin != nil && *ev.ActorLogin != "" {
		return fmt.Sprintf("%s by %s", base, *ev.ActorLogin)
	}
	src := ev.Source
	if src == "" {
		src = "system"
	}
	return fmt.Sprintf("%s (%s)", base, src)
}

// Lookup returns the event key and summary for an audit action, and whether the
// action is subscribable. Non-subscribable actions short-circuit enqueue with
// zero DB work (spec § 7.1 step a).
func Lookup(action string) (EventKey, func(ev NotifyEvent) string, bool) {
	entry, ok := catalog[action]
	if !ok {
		return "", nil, false
	}
	return entry.event, entry.summarise, true
}

// CatalogEvents returns the sorted set of subscribable event keys. Used by the
// subscription validator to reject unknown keys in the events array.
func CatalogEvents() []EventKey {
	seen := map[EventKey]struct{}{}
	for _, e := range catalog {
		seen[e.event] = struct{}{}
	}
	out := make([]EventKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// IsCatalogEvent reports whether key is a valid subscribable event key.
func IsCatalogEvent(key string) bool {
	for _, e := range catalog {
		if string(e.event) == key {
			return true
		}
	}
	return false
}
