package notify

import "testing"

func TestLookupMapsRealActions(t *testing.T) {
	cases := map[string]EventKey{
		"delete_app_confirm": "app.deleted",
		"abuse_detected":     "abuse.detected",
		"member_invite":      "member.added",
		"member_remove":      "member.removed",
		"token_revoke":       "token.revoked",
	}
	for action, want := range cases {
		got, summ, ok := Lookup(action)
		if !ok {
			t.Fatalf("Lookup(%q) ok = false, want true", action)
		}
		if got != want {
			t.Errorf("Lookup(%q) event = %q, want %q", action, got, want)
		}
		if summ == nil {
			t.Errorf("Lookup(%q) summariser is nil", action)
		}
	}
}

func TestLookupRejectsNonSubscribable(t *testing.T) {
	for _, action := range []string{"delete_app_preview", "webhook_received", "login", "redeploy_triggered", ""} {
		if _, _, ok := Lookup(action); ok {
			t.Errorf("Lookup(%q) ok = true, want false (not subscribable)", action)
		}
	}
}

func TestSummariserUsesActorNotArgs(t *testing.T) {
	actor := "alice"
	_, summ, _ := Lookup("delete_app_confirm")
	got := summ(NotifyEvent{ActorLogin: &actor, Source: "user"})
	if got != "App deleted by alice" {
		t.Errorf("summary = %q, want %q", got, "App deleted by alice")
	}
	// system event (no actor) falls back to source, never to args.
	gotSys := summ(NotifyEvent{Source: "system"})
	if gotSys != "App deleted (system)" {
		t.Errorf("system summary = %q, want %q", gotSys, "App deleted (system)")
	}
}

func TestCatalogEventsAndMembership(t *testing.T) {
	events := CatalogEvents()
	if len(events) == 0 {
		t.Fatal("CatalogEvents is empty")
	}
	if !IsCatalogEvent("app.deleted") {
		t.Error("app.deleted should be a catalog event")
	}
	if IsCatalogEvent("not.a.real.event") {
		t.Error("unknown key should not be a catalog event")
	}
}
