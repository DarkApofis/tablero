package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/DarkApofis/tablero/backend/internal/service"
)

func handleHealth(health *service.HealthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := health.Check(r.Context()); err != nil {
			slog.Warn("health check failed", "err", err, "request_id", RequestID(r.Context()))
			WriteError(w, http.StatusServiceUnavailable, CodeDBUnavailable, "database is unreachable")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
