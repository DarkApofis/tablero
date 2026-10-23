package httpapi

import (
	"net/http"

	"github.com/DarkApofis/tablero/backend/internal/service"
)

type Deps struct {
	CORSOrigins []string
	Health      *service.HealthService
}

// NewRouter registers the routes and wraps them in the middleware chain:
// request id → logging → recover → CORS → mux.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth(d.Health))

	var h http.Handler = withJSONMuxErrors(mux)
	h = withCORS(d.CORSOrigins, h)
	h = withRecover(h)
	h = withLogging(h)
	h = withRequestID(h)
	return h
}

// withJSONMuxErrors replaces the mux's plain-text 404 and 405 replies with the
// uniform JSON error. It runs the mux's own fallback handler against a writer
// that discards the body, so headers such as Allow are kept.
func withJSONMuxErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		probe := &discardWriter{header: w.Header()}
		h.ServeHTTP(probe, r)
		switch probe.status {
		case http.StatusMethodNotAllowed:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		case http.StatusNotFound, 0:
			WriteError(w, http.StatusNotFound, CodeNotFound, "route not found")
		default:
			// Redirects (e.g. path cleaning) and anything else: replay as-is.
			mux.ServeHTTP(w, r)
		}
	})
}

type discardWriter struct {
	header http.Header
	status int
}

func (d *discardWriter) Header() http.Header         { return d.header }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(status int)      { d.status = status }
