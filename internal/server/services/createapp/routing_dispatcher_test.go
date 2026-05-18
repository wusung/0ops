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
