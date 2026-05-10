package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/shared"
)

func TestHandlerReturnsStatusAndVersion(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if body["status"] != "ok" {
		t.Fatalf("body status = %q, want %q", body["status"], "ok")
	}
	if body["version"] != shared.Version {
		t.Fatalf("body version = %q, want %q", body["version"], shared.Version)
	}
}
