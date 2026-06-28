package notify

import (
	"strings"
	"testing"
	"time"
)

func sampleEvent() NotifyEvent {
	actor := "alice"
	subj := "11111111-1111-1111-1111-111111111111"
	trace := "abc123"
	return NotifyEvent{
		AuditLogID:  42,
		TeamID:      "team-uuid",
		TeamSlug:    "acme",
		Action:      "delete_app_confirm",
		Source:      "user",
		SubjectType: "app",
		SubjectID:   &subj,
		ActorLogin:  &actor,
		Outcome:     "success",
		OccurredAt:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		TraceID:     &trace,
	}
}

func TestBuildPayloadWhitelistFields(t *testing.T) {
	ev := sampleEvent()
	p := BuildPayload("del-1", "app.deleted", "App deleted by alice", ev)
	if p.DeliveryID != "del-1" || p.Event != "app.deleted" || p.AuditID != 42 {
		t.Fatalf("payload core fields wrong: %+v", p)
	}
	if p.TeamSlug != "acme" || p.Source != "user" || p.SubjectType != "app" || p.Outcome != "success" {
		t.Fatalf("payload metadata wrong: %+v", p)
	}
	if p.Actor == nil || *p.Actor != "alice" {
		t.Fatalf("actor not set: %+v", p.Actor)
	}
	if p.Summary != "App deleted by alice" {
		t.Fatalf("summary = %q", p.Summary)
	}
}

func TestMarshalPayloadIsStableAcrossCalls(t *testing.T) {
	p := BuildPayload("del-1", "app.deleted", "s", sampleEvent())
	a, err := MarshalPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := MarshalPayload(p)
	if string(a) != string(b) {
		t.Fatalf("payload bytes not stable:\n%s\n%s", a, b)
	}
}

func TestPayloadHasNoForbiddenKeysOrAuditPayload(t *testing.T) {
	p := BuildPayload("del-1", "app.deleted", "App deleted by alice", sampleEvent())
	body, err := MarshalPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	if !AssertNoForbiddenKeys(body) {
		t.Fatalf("payload tripped forbidden-key guard: %s", body)
	}
	// Explicit: no args / result keys leaked into the outbound body.
	for _, bad := range []string{`"args"`, `"result"`, `"secret"`, `"token"`, `"signature"`} {
		if strings.Contains(string(body), bad) {
			t.Errorf("payload contains forbidden key %s: %s", bad, body)
		}
	}
}

func TestAssertNoForbiddenKeysDetectsLeak(t *testing.T) {
	if AssertNoForbiddenKeys([]byte(`{"secret_key":"x"}`)) {
		t.Error("guard failed to detect a secret key")
	}
	if AssertNoForbiddenKeys([]byte(`{"nested":{"api_token":"x"}}`)) {
		t.Error("guard failed to detect a nested token key")
	}
}
