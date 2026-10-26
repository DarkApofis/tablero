package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Pinger checks that the database answers; *pgxpool.Pool is one.
type Pinger interface {
	Ping(ctx context.Context) error
}

// handleHealth is liveness: the process is up and serving. It touches nothing
// else, so a database outage never gets a healthy API restarted.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is readiness: the API can do its work, which needs the database,
// and is not on its way out. While it answers 503, traffic should go elsewhere.
func handleReady(db Pinger, shuttingDown *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if shuttingDown != nil && shuttingDown.Load() {
			WriteError(w, http.StatusServiceUnavailable, CodeShuttingDown, "shutting down")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			slog.Warn("readiness check failed", "err", err, "request_id", RequestID(r.Context()))
			WriteError(w, http.StatusServiceUnavailable, CodeDBUnavailable, "database is unreachable")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
