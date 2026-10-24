package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger checks that the database answers; *pgxpool.Pool is one.
type Pinger interface {
	Ping(ctx context.Context) error
}

func handleHealth(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			slog.Warn("health check failed", "err", err, "request_id", RequestID(r.Context()))
			WriteError(w, http.StatusServiceUnavailable, CodeDBUnavailable, "database is unreachable")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
