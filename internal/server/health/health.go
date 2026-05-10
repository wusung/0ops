package health

import (
	"encoding/json"
	"net/http"

	"github.com/winshare/zeroops/internal/shared"
)

func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": shared.Version,
		})
	}
}
