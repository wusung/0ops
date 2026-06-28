package notify

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The production delivery client must refuse to connect to a loopback target
// even when the (already-validated) URL points there at delivery time — this is
// the DNS-rebinding / delivery-side SSRF guard (spec § 6.4). The httptest server
// listens on 127.0.0.1, so a successful request would prove the guard is absent.
func TestSafeDeliveryClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, err := safeDeliveryClient().Get(srv.URL)
	if err == nil {
		t.Fatal("safe delivery client must refuse to dial a loopback target")
	}
}

// The safe client must refuse to follow redirects (a webhook receiver that 302s
// to an internal endpoint must not be chased).
func TestSafeDeliveryClientRefusesRedirect(t *testing.T) {
	// Public-looking target is impossible to dial in a unit test, so assert the
	// CheckRedirect policy directly.
	c := safeDeliveryClient()
	if c.CheckRedirect == nil {
		t.Fatal("safe client must set a CheckRedirect policy")
	}
	if err := c.CheckRedirect(nil, nil); err == nil {
		t.Fatal("safe client CheckRedirect must reject redirects")
	}
}
