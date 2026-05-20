package createapp

import (
	"context"
	"errors"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

type recDispatcher struct{ called string }

func (r *recDispatcher) Dispatch(_ context.Context, _ workflowdispatch.ClientPayload) error {
	r.called = "ok"
	return nil
}

type fakeRepoLookup struct {
	url string
	err error
}

func (f fakeRepoLookup) GetAppRepoURLByTeamAndAppSlug(_ context.Context, _, _ string) (string, error) {
	return f.url, f.err
}

func TestRoutingDispatcher_RoutesByScheme(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		err       error
		wantLocal bool
		wantErr   bool
	}{
		{"github https", "https://github.com/x/y", nil, false, false},
		{"github ssh", "git@github.com:x/y.git", nil, false, false},
		{"file scheme", "file:///workspace/examples/node-demo", nil, true, false},
		{"lookup error", "", errors.New("not found"), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := &recDispatcher{}
			gh := &recDispatcher{}
			rd := &RoutingDispatcher{
				GitHubDispatcher: gh,
				LocalDispatcher:  local,
				Lookup:           fakeRepoLookup{url: tc.url, err: tc.err},
			}
			err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{TeamSlug: "t", AppSlug: "a"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLocal {
				if local.called != "ok" {
					t.Errorf("local not called")
				}
				if gh.called != "" {
					t.Errorf("github should not be called")
				}
			} else {
				if gh.called != "ok" {
					t.Errorf("github not called")
				}
				if local.called != "" {
					t.Errorf("local should not be called")
				}
			}
		})
	}
}

func TestRoutingDispatcher_NilGitHubIsToleratedForGitHubURL(t *testing.T) {
	rd := &RoutingDispatcher{
		GitHubDispatcher: nil,
		LocalDispatcher:  &recDispatcher{},
		Lookup:           fakeRepoLookup{url: "https://github.com/x/y"},
	}
	if err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{}); err != nil {
		t.Fatalf("expected nil error (nil-tolerant), got %v", err)
	}
}

// TestRoutingDispatcher_UploadKindUsesUploadDispatcher verifies that
// payload.SourceKind="upload" routes to UploadDispatcher, bypassing Lookup.
func TestRoutingDispatcher_UploadKindUsesUploadDispatcher(t *testing.T) {
	upload := &recDispatcher{}
	gh := &recDispatcher{}
	local := &recDispatcher{}
	rd := &RoutingDispatcher{
		GitHubDispatcher: gh,
		LocalDispatcher:  local,
		UploadDispatcher: upload,
		// Lookup would panic if called — it's nil here intentionally.
		Lookup: nil,
	}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		SourceKind: "upload",
		TeamSlug:   "t",
		AppSlug:    "a",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if upload.called != "ok" {
		t.Errorf("upload dispatcher not called")
	}
	if gh.called != "" {
		t.Errorf("github should not be called")
	}
	if local.called != "" {
		t.Errorf("local should not be called")
	}
}

// TestRoutingDispatcher_GitHubKindUsesGitHubDispatcher verifies that
// payload.SourceKind="github" routes to GitHubDispatcher, bypassing Lookup.
func TestRoutingDispatcher_GitHubKindUsesGitHubDispatcher(t *testing.T) {
	gh := &recDispatcher{}
	upload := &recDispatcher{}
	rd := &RoutingDispatcher{
		GitHubDispatcher: gh,
		UploadDispatcher: upload,
		// Lookup would panic if called — it's nil here intentionally.
		Lookup: nil,
	}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		SourceKind: "github",
		TeamSlug:   "t",
		AppSlug:    "a",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if gh.called != "ok" {
		t.Errorf("github dispatcher not called")
	}
	if upload.called != "" {
		t.Errorf("upload should not be called")
	}
}

// TestRoutingDispatcher_LocalKindUsesLocalDispatcher verifies that
// payload.SourceKind="local" routes to LocalDispatcher, bypassing Lookup.
func TestRoutingDispatcher_LocalKindUsesLocalDispatcher(t *testing.T) {
	local := &recDispatcher{}
	gh := &recDispatcher{}
	rd := &RoutingDispatcher{
		GitHubDispatcher: gh,
		LocalDispatcher:  local,
		// Lookup would panic if called — it's nil here intentionally.
		Lookup: nil,
	}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		SourceKind: "local",
		TeamSlug:   "t",
		AppSlug:    "a",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if local.called != "ok" {
		t.Errorf("local dispatcher not called")
	}
	if gh.called != "" {
		t.Errorf("github should not be called")
	}
}

// TestRoutingDispatcher_UploadKindNilReturnsError verifies that SourceKind="upload"
// with no UploadDispatcher configured returns an error.
func TestRoutingDispatcher_UploadKindNilReturnsError(t *testing.T) {
	rd := &RoutingDispatcher{
		GitHubDispatcher: &recDispatcher{},
		UploadDispatcher: nil,
		Lookup:           fakeRepoLookup{url: "https://github.com/x/y"},
	}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		SourceKind: "upload",
	})
	if err == nil {
		t.Fatal("expected error when UploadDispatcher is nil, got nil")
	}
}

// TestRoutingDispatcher_LocalKindNilFallsThroughToURLLookup verifies that
// SourceKind="local" with a nil LocalDispatcher falls through to the legacy
// URL-prefix lookup (which then dispatches to GitHubDispatcher).
func TestRoutingDispatcher_LocalKindNilFallsThroughToURLLookup(t *testing.T) {
	gh := &recDispatcher{}
	rd := &RoutingDispatcher{
		GitHubDispatcher: gh,
		LocalDispatcher:  nil,
		UploadDispatcher: nil,
		Lookup:           fakeRepoLookup{url: "https://github.com/foo/bar"},
	}
	// SourceKind="local" but no LocalDispatcher → fall through → URL lookup → github dispatch.
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		TeamSlug:   "t",
		AppSlug:    "a",
		SourceKind: "local",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if gh.called != "ok" {
		t.Fatalf("expected github dispatch (legacy fallback), got calls=%q", gh.called)
	}
}

// TestRoutingDispatcher_LegacyURLFallback verifies that when SourceKind is empty,
// the legacy URL-prefix lookup still works (file:// → LocalDispatcher).
func TestRoutingDispatcher_LegacyURLFallback(t *testing.T) {
	local := &recDispatcher{}
	gh := &recDispatcher{}
	rd := &RoutingDispatcher{
		GitHubDispatcher: gh,
		LocalDispatcher:  local,
		Lookup:           fakeRepoLookup{url: "file:///workspace/app"},
	}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		// SourceKind intentionally empty (legacy payload)
		TeamSlug: "t",
		AppSlug:  "a",
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if local.called != "ok" {
		t.Errorf("local dispatcher not called for legacy file:// URL")
	}
	if gh.called != "" {
		t.Errorf("github should not be called for file:// URL")
	}
}
