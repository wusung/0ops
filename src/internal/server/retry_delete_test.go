package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestAdminRetryDeleteSuccess(t *testing.T) {
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, "").RetryStuckDelete(context.Background(), dto.AdminRetryDeleteRequest{
		TeamSlug: "acme",
		AppSlug:  "nextdemo",
	})
	if err != nil {
		t.Fatalf("RetryStuckDelete() error = %v", err)
	}
	if out.JobID != "job-retry-1" {
		t.Errorf("JobID = %q, want job-retry-1", out.JobID)
	}
	if out.AppSlug != "nextdemo" || out.Status != "deleting" {
		t.Errorf("unexpected response: %+v", out)
	}
	if len(store.retryDeleteCalls) != 1 || store.retryDeleteCalls[0] != "acme/nextdemo" {
		t.Errorf("store call = %v, want [acme/nextdemo]", store.retryDeleteCalls)
	}
}

func TestAdminRetryDeleteValidation(t *testing.T) {
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, "").RetryStuckDelete(context.Background(), dto.AdminRetryDeleteRequest{
		TeamSlug: "acme",
		// AppSlug missing
	})
	if err == nil || !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("error = %v, want validation_failed", err)
	}
	if len(store.retryDeleteCalls) != 0 {
		t.Errorf("store should not be called on validation failure, got %v", store.retryDeleteCalls)
	}
}

func TestAdminRetryDeleteNotDeleting(t *testing.T) {
	store, _ := newFakeStore()
	store.retryDeleteErr = db.ErrAppNotDeleting
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, "").RetryStuckDelete(context.Background(), dto.AdminRetryDeleteRequest{
		TeamSlug: "acme",
		AppSlug:  "live-app",
	})
	if err == nil || !strings.Contains(err.Error(), "app_not_deleting") {
		t.Fatalf("error = %v, want app_not_deleting", err)
	}
}

func TestAdminRetryDeleteAppNotFound(t *testing.T) {
	store, _ := newFakeStore()
	store.retryDeleteErr = db.ErrAppNotFound
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, "").RetryStuckDelete(context.Background(), dto.AdminRetryDeleteRequest{
		TeamSlug: "acme",
		AppSlug:  "ghost",
	})
	if err == nil || !strings.Contains(err.Error(), "app_not_found") {
		t.Fatalf("error = %v, want app_not_found", err)
	}
}
