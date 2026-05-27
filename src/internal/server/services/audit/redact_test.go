package audit

import (
	"reflect"
	"testing"
)

func TestRedactRemovesSecretKeys(t *testing.T) {
	t.Parallel()

	in := map[string]any{
		"slug":          "nextdemo",
		"repo_url":      "https://github.com/x/y",
		"token":         "op_pat_xxxx",
		"GHA_TOKEN":     "secret",
		"my_secret":     "shh",
		"api_secret":    "shh-too",
		"x_signature":   "deadbeef",
		"private_key":   "RSA...",
		"Authorization": "Bearer x.y.z",
		"COOKIE":        "session=abc",
		"nested": map[string]any{
			"password": "p4ss",
			"safe":     "ok",
		},
		"list": []any{
			map[string]any{"token": "tok", "name": "ci"},
			"plain",
		},
	}

	out := Redact(in).(map[string]any)

	for _, key := range []string{"token", "GHA_TOKEN", "my_secret", "api_secret", "x_signature", "private_key", "Authorization", "COOKIE"} {
		if got, ok := out[key].(string); !ok || got != "***" {
			t.Fatalf("expected %q to be redacted, got %#v", key, out[key])
		}
	}

	if out["slug"] != "nextdemo" {
		t.Fatalf("non-sensitive slug was modified: %#v", out["slug"])
	}

	nested := out["nested"].(map[string]any)
	if nested["password"] != "***" {
		t.Fatalf("nested password not redacted: %#v", nested)
	}
	if nested["safe"] != "ok" {
		t.Fatalf("nested safe value was modified: %#v", nested)
	}

	list := out["list"].([]any)
	first := list[0].(map[string]any)
	if first["token"] != "***" {
		t.Fatalf("list item token not redacted: %#v", first)
	}
	if first["name"] != "ci" {
		t.Fatalf("list item name was modified: %#v", first)
	}
	if list[1] != "plain" {
		t.Fatalf("list scalar was modified: %#v", list[1])
	}
}

func TestRedactPreservesNil(t *testing.T) {
	t.Parallel()
	if got := Redact(nil); got != nil {
		t.Fatalf("Redact(nil) = %#v, want nil", got)
	}
}

func TestRedactSurvivesUnknownTypes(t *testing.T) {
	t.Parallel()
	in := struct{ Name string }{Name: "ok"}
	out := Redact(in)
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("unknown type changed: in=%#v out=%#v", in, out)
	}
}
