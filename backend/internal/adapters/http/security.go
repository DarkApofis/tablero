package httpapi

import (
	"net"
	"net/http"
	"slices"
	"sync"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// maxRequestBytes caps every request body. The largest legitimate one, a task
// with a 10,000-character description, is well under it.
const maxRequestBytes = 256 << 10

// withBodyLimit caps the body of every request; reading past it fails (and
// decodeJSON answers 413).
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders sets the headers every API response carries. The API
// only serves JSON and event streams, so its CSP allows nothing: a response
// opened as a page can run nothing, load nothing and be framed nowhere.
// HSTS goes out only over HTTPS (directly, or as a trusted proxy reports it).
func withSecurityHeaders(trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil || (trustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// withCSRFCheck rejects state-changing requests that a browser sent from a
// page outside CORS_ORIGINS (docs/DECISIONS.md #10). Browsers send Origin on
// every POST, PUT, PATCH and DELETE; without it, Sec-Fetch-Site still says
// whether the request is cross-site. Requests with neither come from
// non-browser clients, which cannot be tricked into sending a victim's cookies.
func withCSRFCheck(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !slices.Contains(allowedOrigins, origin) {
				WriteError(w, http.StatusForbidden, CodeCSRFRejected, "Origen no permitido")
				return
			}
		} else if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
			WriteError(w, http.StatusForbidden, CodeCSRFRejected, "Origen no permitido")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CookieOptions are the attributes of the session cookies, which are always
// HttpOnly.
type CookieOptions struct {
	Secure   bool
	SameSite http.SameSite // zero: Lax
	Domain   string        // empty: the API's host only
	// PathPrefix goes before each cookie's path when a proxy serves the API
	// under a prefix (the frontend forwarding /api/* here): "/api" makes the
	// refresh cookie's path /api/auth, where the browser sends it.
	PathPrefix string
}

func (o CookieOptions) cookie(name, value, path string) *http.Cookie {
	sameSite := o.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	// #nosec G124 -- HttpOnly always; Secure comes from COOKIE_SECURE (true by
	// default; false only for plain-http development); SameSite from COOKIE_SAMESITE.
	return &http.Cookie{Name: name, Value: value, Path: o.PathPrefix + path, Domain: o.Domain, HttpOnly: true, Secure: o.Secure, SameSite: sameSite}
}

// StreamLimits cap open event streams on this instance; zero means no cap.
type StreamLimits struct {
	PerUser  int
	PerBoard int
}

// streamCounter counts open streams per user and per board.
type streamCounter struct {
	limits StreamLimits

	mu      sync.Mutex
	byUser  map[domain.ID]int
	byBoard map[domain.ID]int
}

func newStreamCounter(l StreamLimits) *streamCounter {
	return &streamCounter{limits: l, byUser: map[domain.ID]int{}, byBoard: map[domain.ID]int{}}
}

// acquire counts a stream of user on board, unless either is at its limit;
// release must be called once the stream ends.
func (c *streamCounter) acquire(user, board domain.ID) (release func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.limits.PerUser > 0 && c.byUser[user] >= c.limits.PerUser) ||
		(c.limits.PerBoard > 0 && c.byBoard[board] >= c.limits.PerBoard) {
		return nil, false
	}
	c.byUser[user]++
	c.byBoard[board]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.byUser[user]--; c.byUser[user] == 0 {
				delete(c.byUser, user)
			}
			if c.byBoard[board]--; c.byBoard[board] == 0 {
				delete(c.byBoard, board)
			}
		})
	}, true
}

// limitKey is the rate-limit key of an IP: IPv6 clients usually hold a whole
// /64, so they are counted by it.
func limitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
