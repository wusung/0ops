package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	token      db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	memberRows []db.Member

	members    bool
	hasOwner   bool
	previews   map[string]db.Preview
	previewSeq int
}

func (f *fakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, errors.New("not found")
	}
	return f.token, nil
}

func (f *fakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}

func (f *fakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f *fakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}

func (f *fakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	out := make([]db.App, 0, len(f.apps))
	for _, app := range f.apps {
		if afterID != nil && app.ID <= *afterID {
			continue
		}
		out = append(out, app)
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) HasAnyOwner(ctx context.Context) (bool, error) {
	return f.hasOwner, nil
}

func (f *fakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	if f.hasOwner {
		return "", "", db.ErrBootstrapAlreadyDone
	}
	f.hasOwner = true
	return "team-bootstrap", "user-bootstrap", nil
}

func (f *fakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	return append([]db.Member(nil), f.memberRows...), nil
}

func (f *fakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	f.previewSeq++
	id := "preview-" + strings.TrimSpace(time.Now().UTC().Format("150405")) + "-" + string(rune('a'+f.previewSeq))
	p := db.Preview{
		ID:          id,
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        args,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
	}
	f.previews[id] = p
	return p, nil
}

func (f *fakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	p, ok := f.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return p, nil
}

func (f *fakeStore) ConsumePreview(ctx context.Context, previewID string) error {
	p, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	if p.ConsumedAt != nil {
		return db.ErrPreviewConsumed
	}
	now := time.Now().UTC()
	p.ConsumedAt = &now
	f.previews[previewID] = p
	return nil
}

func (f *fakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	member := db.Member{
		UserID:      "new-user",
		GithubLogin: params.GithubLogin,
		Email:       params.Email,
		Role:        params.Role,
		JoinedAt:    timePtr(time.Now().UTC()),
		InvitedAt:   timePtr(time.Now().UTC()),
	}
	f.memberRows = append(f.memberRows, member)
	return member, nil
}

func (f *fakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	for _, row := range f.memberRows {
		if row.UserID == targetUserID && row.Role == "owner" {
			return db.ErrOwnerRemoval
		}
	}
	next := make([]db.Member, 0, len(f.memberRows))
	removed := false
	for _, row := range f.memberRows {
		if row.UserID == targetUserID {
			removed = true
			continue
		}
		next = append(next, row)
	}
	if !removed {
		return db.ErrMemberNotFound
	}
	f.memberRows = next
	return nil
}

func TestNewRouterListApps(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	out, err := client.ListApps(context.Background(), store.team.Slug, 50, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(out.Items))
	}
	if out.Items[0].Slug != "alpha" || out.Items[1].Slug != "beta" {
		t.Fatalf("unexpected items: %#v", out.Items)
	}
}

func TestNewRouterListAppsRejectsWrongTeam(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/wrong/apps", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestListAppsPagination(t *testing.T) {
	store, token := newFakeStore()
	store.apps = append(store.apps, db.App{
		ID:        "4",
		TeamID:    store.team.ID,
		Slug:      "gamma",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	out, err := client.ListApps(context.Background(), store.team.Slug, 2, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	if out.NextCursor == nil {
		t.Fatal("expected next cursor")
	}
	cursor, err := decodeAppCursor(*out.NextCursor)
	if err != nil {
		t.Fatalf("decodeAppCursor() error = %v", err)
	}
	if cursor == nil || *cursor != "2" {
		t.Fatalf("decoded cursor = %#v, want 2", cursor)
	}
}

func TestRouterJSONShape(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListApps(context.Background(), store.team.Slug, 50, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("response is not valid json")
	}
	var decoded dto.ListAppsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}

func TestBootstrapOwnerOneShot(t *testing.T) {
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, "")
	_, err := client.BootstrapOwner(context.Background(), dto.BootstrapOwnerRequest{
		TeamSlug:    "acme-bootstrap",
		TeamName:    "Acme Bootstrap",
		GithubLogin: "owner-login",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() first call error = %v", err)
	}

	_, err = client.BootstrapOwner(context.Background(), dto.BootstrapOwnerRequest{
		TeamSlug:    "acme-bootstrap",
		TeamName:    "Acme Bootstrap",
		GithubLogin: "owner-login",
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_already_done") {
		t.Fatalf("second bootstrap error = %v, want bootstrap_already_done", err)
	}
}

func TestMembersPreviewInviteAndInvite(t *testing.T) {
	store, token := newFakeStore()
	store.role = "admin"
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewInviteMember(context.Background(), store.team.Slug, dto.InviteMemberRequest{
		GithubLogin: strPtr("new-member"),
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("PreviewInviteMember() error = %v", err)
	}
	if preview.PreviewID == "" {
		t.Fatal("expected preview id")
	}

	invited, err := client.InviteMember(context.Background(), store.team.Slug, dto.ConfirmInviteMemberRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("InviteMember() error = %v", err)
	}
	if invited.Member.Role != "member" {
		t.Fatalf("member role = %q, want member", invited.Member.Role)
	}
}

func TestMembersListForbiddenForViewer(t *testing.T) {
	store, token := newFakeStore()
	store.role = "viewer"
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).ListMembers(context.Background(), store.team.Slug)
	if err == nil || !strings.Contains(err.Error(), "forbidden_role") {
		t.Fatalf("ListMembers() error = %v, want forbidden_role", err)
	}
}

func newFakeStore() (*fakeStore, string) {
	token := "dev-token"
	store := &fakeStore{
		token: db.CliToken{
			ID:          "token-1",
			OwnerUserID: "user-1",
			TeamID:      "team-1",
			TokenHash:   auth.HashBearerToken(token),
			Scopes:      []string{"apps:read", "members:manage"},
		},
		team: db.Team{
			ID:   "team-1",
			Slug: "acme",
			Name: "Acme",
		},
		role:    "admin",
		members: true,
		apps: []db.App{
			{
				ID:        "1",
				TeamID:    "team-1",
				Slug:      "alpha",
				Name:      strPtr("Alpha"),
				Status:    strPtr("ready"),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
			{
				ID:        "2",
				TeamID:    "team-1",
				Slug:      "beta",
				Name:      strPtr("Beta"),
				Status:    strPtr("ready"),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
		},
		memberRows: []db.Member{
			{
				UserID:      "user-1",
				GithubLogin: strPtr("owner"),
				Role:        "owner",
				JoinedAt:    timePtr(time.Now().UTC()),
			},
		},
		previews: make(map[string]db.Preview),
	}
	return store, token
}

func strPtr(v string) *string { return &v }

func timePtr(v time.Time) *time.Time { return &v }
