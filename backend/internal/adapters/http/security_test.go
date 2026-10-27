package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/adapters/token"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const frontend = "http://localhost:3000"

func secureRouter(t *testing.T, mod func(*Deps)) (http.Handler, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	d := Deps{
		CORSOrigins: []string{frontend},
		Ready:       fakePinger{},
		Auth:        newAuthService(store, token.NewIssuer(testSecret, 15*time.Minute), time.Hour),
		AuthLimits:  &DefaultAuthLimits,
	}
	if mod != nil {
		mod(&d)
	}
	return NewRouter(d), store
}

func send(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i] == "RemoteAddr" {
			req.RemoteAddr = headers[i+1]
			continue
		}
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := secureRouter(t, func(d *Deps) { d.TrustProxy = true })
	for _, path := range []string{"/health", "/nope"} {
		hdr := send(h, "GET", path, "").Header()
		for name, want := range map[string]string{
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := hdr.Get(name); got != want {
				t.Errorf("%s %s = %q, want %q", path, name, got, want)
			}
		}
		if hdr.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: HSTS over plain http", path)
		}
	}
	if send(h, "GET", "/health", "", "X-Forwarded-Proto", "https").Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS behind a TLS-terminating proxy")
	}
	// Without TrustProxy the header is the client's word: ignored.
	h, _ = secureRouter(t, nil)
	if send(h, "GET", "/health", "", "X-Forwarded-Proto", "https").Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS from an untrusted X-Forwarded-Proto")
	}
}

func TestCSRFCheck(t *testing.T) {
	h, _ := secureRouter(t, func(d *Deps) { d.AuthLimits = nil })
	login := `{"email":"nadie@example.com","password":"x"}`
	for _, tc := range []struct {
		name    string
		headers []string
		want    int
	}{
		{"allowed origin", []string{"Origin", frontend}, http.StatusUnauthorized},
		{"no browser headers (curl, server to server)", nil, http.StatusUnauthorized},
		{"same-origin fetch metadata", []string{"Sec-Fetch-Site", "same-origin"}, http.StatusUnauthorized},
		{"another origin", []string{"Origin", "https://evil.example"}, http.StatusForbidden},
		{"opaque origin (sandboxed iframe, file:)", []string{"Origin", "null"}, http.StatusForbidden},
		{"cross-site without Origin", []string{"Sec-Fetch-Site", "cross-site"}, http.StatusForbidden},
		{"same-site, other subdomain", []string{"Sec-Fetch-Site", "same-site"}, http.StatusForbidden},
	} {
		rec := send(h, "POST", "/auth/login", login, tc.headers...)
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
		if tc.want == http.StatusForbidden && decodeError(t, rec).Code != CodeCSRFRejected {
			t.Errorf("%s: %s", tc.name, rec.Body)
		}
	}
	// Cookie-only endpoints are covered too, and reads are not.
	if rec := send(h, "POST", "/auth/refresh", "", "Origin", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin refresh: %d", rec.Code)
	}
	if rec := send(h, "GET", "/health", "", "Origin", "https://evil.example"); rec.Code != http.StatusOK {
		t.Errorf("cross-origin GET: %d", rec.Code)
	}
}

func TestBodiesMustBeSmallJSON(t *testing.T) {
	h, _ := secureRouter(t, func(d *Deps) { d.AuthLimits = nil })

	// A form post (or text/plain, which needs no CORS preflight) is refused.
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"a@b.c","password":"x"}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType || decodeError(t, rec).Code != CodeUnsupportedMedia {
			t.Errorf("Content-Type %q: %d %s", ct, rec.Code, rec.Body)
		}
	}
	if rec := send(h, "POST", "/auth/login", `{"email":"a@b.c","password":"x"}`, "Content-Type", "application/json; charset=utf-8"); rec.Code != http.StatusUnauthorized {
		t.Errorf("JSON with charset: %d", rec.Code)
	}

	big := `{"email":"a@b.c","password":"` + strings.Repeat("x", maxRequestBytes) + `"}`
	if rec := send(h, "POST", "/auth/login", big); rec.Code != http.StatusRequestEntityTooLarge || decodeError(t, rec).Code != CodeBodyTooLarge {
		t.Errorf("oversized body: %d %s", rec.Code, rec.Body)
	}
}

func TestLoginRateLimits(t *testing.T) {
	h, _ := secureRouter(t, nil)
	body := func(email, password string) string {
		return `{"email":"` + email + `","password":"` + password + `"}`
	}
	if rec := send(h, "POST", "/auth/register", `{"email":"ana@example.com","name":"Ana","password":"correcta-123"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}

	// Per account: after 10 failures even the right password waits…
	for i := range DefaultAuthLimits.LoginEmail.Burst {
		ip := "10.0.0." + string(rune('1'+i%9)) + ":1234" // different IPs: only the account limit applies
		if rec := send(h, "POST", "/auth/login", body("ana@example.com", "mal"), "RemoteAddr", ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, rec.Code)
		}
	}
	rec := send(h, "POST", "/auth/login", body("ANA@example.com ", "correcta-123"), "RemoteAddr", "10.0.1.1:1234")
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec).Code != CodeRateLimited || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th attempt: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	// …while other accounts are unaffected.
	if rec := send(h, "POST", "/auth/login", body("otra@example.com", "mal"), "RemoteAddr", "10.0.1.1:1234"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another account: %d", rec.Code)
	}

	// Per IP: every attempt counts, whatever the account.
	h, _ = secureRouter(t, nil)
	for i := range DefaultAuthLimits.LoginIP.Burst {
		email := "persona" + string(rune('a'+i)) + "@example.com"
		if rec := send(h, "POST", "/auth/login", body(email, "mal"), "RemoteAddr", "192.0.2.7:5555"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
	}
	if rec := send(h, "POST", "/auth/login", body("z@example.com", "mal"), "RemoteAddr", "192.0.2.7:6666"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP over the limit: %d", rec.Code)
	}
	// A forged X-Forwarded-For does not get around it (no TrustProxy).
	if rec := send(h, "POST", "/auth/login", body("z@example.com", "mal"), "RemoteAddr", "192.0.2.7:6666", "X-Forwarded-For", "203.0.113.9"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("forged X-Forwarded-For: %d", rec.Code)
	}
	if rec := send(h, "POST", "/auth/login", body("z@example.com", "mal"), "RemoteAddr", "192.0.2.8:5555"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another IP: %d", rec.Code)
	}
}

func TestRegisterRateLimits(t *testing.T) {
	h, _ := secureRouter(t, nil)
	reg := func(email, ip string) int {
		return send(h, "POST", "/auth/register", `{"email":"`+email+`","name":"X","password":"corta"}`, "RemoteAddr", ip).Code
	}
	for i := range DefaultAuthLimits.RegisterEmail.Burst {
		if code := reg("mismo@example.com", "198.51.100."+string(rune('1'+i))+":1"); code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	if code := reg("mismo@example.com", "198.51.100.9:1"); code != http.StatusTooManyRequests {
		t.Errorf("same email again: %d", code)
	}
	for i := range DefaultAuthLimits.RegisterIP.Burst {
		if code := reg("p"+string(rune('a'+i))+"@example.com", "198.51.100.200:1"); code != http.StatusUnprocessableEntity {
			t.Fatalf("IP attempt %d: %d", i+1, code)
		}
	}
	if code := reg("otra@example.com", "198.51.100.200:1"); code != http.StatusTooManyRequests {
		t.Errorf("IP over the limit: %d", code)
	}
}

func TestLimiterRefillsAndForgets(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(Rate{Burst: 2, Every: time.Minute}, func() time.Time { return now })
	l.Take("k")
	l.Take("k")
	if ok, wait := l.Take("k"); ok || wait != time.Minute {
		t.Errorf("empty bucket: %v %v", ok, wait)
	}
	now = now.Add(30 * time.Second)
	if ok, wait := l.Check("k"); ok || wait != 30*time.Second {
		t.Errorf("half a token: %v %v", ok, wait)
	}
	now = now.Add(30 * time.Second)
	if ok, _ := l.Take("k"); !ok {
		t.Error("a token after a minute")
	}
	// Refilled buckets are dropped at the next sweep.
	now = now.Add(5 * time.Minute)
	l.Take("other")
	if n := l.size(); n != 1 {
		t.Errorf("%d buckets after the sweep, want 1", n)
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:4444"
	req.Header.Add("X-Forwarded-For", "1.1.1.1, 203.0.113.5")
	if got := clientIP(req, 0); got != "10.1.2.3" {
		t.Errorf("untrusted: %s", got)
	}
	// One proxy: the last entry is the one it added; the first is the client's word.
	if got := clientIP(req, 1); got != "203.0.113.5" {
		t.Errorf("one hop: %s", got)
	}
	// Two (Vercel → Render): the first one put the client second to last.
	req.Header.Add("X-Forwarded-For", "76.76.21.21")
	if got := clientIP(req, 2); got != "203.0.113.5" {
		t.Errorf("two hops, header repeated: %s", got)
	}
	if got := clientIP(req, 9); got != "1.1.1.1" {
		t.Errorf("more hops than entries: %s", got)
	}
	if got := limitKey("2001:db8:1:2:aaaa::1"); got != limitKey("2001:db8:1:2:bbbb::9") || got != "2001:db8:1:2::/64" {
		t.Errorf("IPv6 /64: %s", got)
	}
}

func TestCookieOptions(t *testing.T) {
	h, _ := secureRouter(t, func(d *Deps) {
		d.Cookies = CookieOptions{Secure: true, SameSite: http.SameSiteStrictMode, Domain: "tablero.example.com", PathPrefix: "/api"}
	})
	wantPath := map[string]string{refreshCookieName: "/api/auth", accessCookieName: "/api/boards"}
	rec := send(h, "POST", "/auth/register", `{"email":"c@example.com","name":"C","password":"correcta-123"}`)
	logout := send(h, "POST", "/auth/logout", "")
	for _, r := range []*httptest.ResponseRecorder{rec, logout} {
		cookies := r.Result().Cookies()
		if len(cookies) != 2 {
			t.Fatalf("cookies = %v", cookies)
		}
		for _, c := range cookies {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Domain != "tablero.example.com" || c.Path != wantPath[c.Name] {
				t.Errorf("%s: %+v", c.Name, c)
			}
		}
	}
}

func TestStreamCounter(t *testing.T) {
	c := newStreamCounter(StreamLimits{PerUser: 2, PerBoard: 3})
	ana, bruno, vera := domain.NewID(), domain.NewID(), domain.NewID()
	b1, b2 := domain.NewID(), domain.NewID()

	r1, ok1 := c.acquire(ana, b1)
	_, ok2 := c.acquire(ana, b2)
	if _, ok := c.acquire(ana, b1); !ok1 || !ok2 || ok {
		t.Fatalf("per user: %v %v %v", ok1, ok2, ok)
	}
	c.acquire(bruno, b1)
	c.acquire(vera, b1)
	if _, ok := c.acquire(bruno, b1); ok {
		t.Error("board b1 over its limit")
	}
	r1()
	r1() // idempotent
	if _, ok := c.acquire(ana, b1); !ok {
		t.Error("released stream still counted")
	}
}
