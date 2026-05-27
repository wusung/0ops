// Package health provides the server health endpoint.
package health

import (
	"encoding/json"
	"net/http"

	"github.com/winshare/zeroops/internal/shared"
)

// Handler returns the health check handler.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": shared.Version,
		})
	}
}
