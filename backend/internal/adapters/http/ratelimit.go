package httpapi

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate is a token bucket: up to Burst attempts at once, then one more every
// Every.
type Rate struct {
	Burst int
	Every time.Duration
}

// AuthLimits are the limits on the unauthenticated entry points. Each is kept
// per API instance, in memory (docs/SECURITY.md).
type AuthLimits struct {
	// LoginIP counts every login attempt from an IP; LoginEmail counts only
	// the failed ones against an account, so its owner can still sign in
	// (one try per Every) while someone else is guessing.
	LoginIP, LoginEmail       Rate
	RegisterIP, RegisterEmail Rate
	GuestIP                   Rate
}

// DefaultAuthLimits: a person mistyping is never limited; a script is slowed
// to a crawl.
var DefaultAuthLimits = AuthLimits{
	LoginIP:       Rate{Burst: 20, Every: 3 * time.Second},  // 20/min sustained
	LoginEmail:    Rate{Burst: 10, Every: 90 * time.Second}, // 10 failures per 15 min
	RegisterIP:    Rate{Burst: 10, Every: 6 * time.Minute},  // 10/h
	RegisterEmail: Rate{Burst: 3, Every: 20 * time.Minute},  // 3/h
	GuestIP:       Rate{Burst: 5, Every: 12 * time.Minute},  // 5/h
}

// limiter holds one token bucket per key. Buckets that have refilled are
// dropped now and then, so memory follows the keys active in the last
// Burst*Every, not every key ever seen.
type limiter struct {
	rate Rate
	now  func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(rate Rate, now func() time.Time) *limiter {
	if now == nil {
		now = time.Now
	}
	return &limiter{rate: rate, now: now, buckets: map[string]*bucket{}}
}

// refill returns key's bucket brought up to now (a full one if new).
func (l *limiter) refill(key string, now time.Time) *bucket {
	b, ok := l.buckets[key]
	if !ok {
		return &bucket{tokens: float64(l.rate.Burst), at: now}
	}
	b.tokens = math.Min(float64(l.rate.Burst), b.tokens+float64(now.Sub(b.at))/float64(l.rate.Every))
	b.at = now
	return b
}

// wait is how long until key's bucket has a token, if it has none.
func (l *limiter) wait(b *bucket) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - b.tokens) * float64(l.rate.Every))
}

// Check reports whether key has an attempt left, without using it: if not,
// how long until it does.
func (l *limiter) Check(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.wait(l.refill(key, now))
	return w == 0, w
}

// Take uses one of key's attempts if it has one; if not, it reports how long
// until it does.
func (l *limiter) Take(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b := l.refill(key, now)
	if w := l.wait(b); w > 0 {
		return false, w
	}
	b.tokens--
	l.buckets[key] = b
	return true, 0
}

func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if b.tokens+float64(now.Sub(b.at))/float64(l.rate.Every) >= float64(l.rate.Burst) {
			delete(l.buckets, key)
		}
	}
}

func (l *limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// authLimiter applies AuthLimits; nil limits nothing.
type authLimiter struct {
	loginIP, loginEmail       *limiter
	registerIP, registerEmail *limiter
	guestIP                   *limiter
}

func newAuthLimiter(l AuthLimits, now func() time.Time) *authLimiter {
	return &authLimiter{
		loginIP: newLimiter(l.LoginIP, now), loginEmail: newLimiter(l.LoginEmail, now),
		registerIP: newLimiter(l.RegisterIP, now), registerEmail: newLimiter(l.RegisterEmail, now),
		guestIP: newLimiter(l.GuestIP, now),
	}
}

// writeRateLimited answers 429 with Retry-After.
func writeRateLimited(w http.ResponseWriter, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	when := fmt.Sprintf("%d s", secs)
	if secs > 90 {
		when = fmt.Sprintf("%d min", (secs+59)/60)
	}
	WriteError(w, http.StatusTooManyRequests, CodeRateLimited, "Demasiados intentos. Vuelve a intentarlo en "+when+".")
}

// clientIP is the address the request came from: the connection's peer, or,
// behind proxyHops trusted reverse proxies (0: none), the X-Forwarded-For
// entry that many from the end. Each proxy appends the address it saw, so
// that one was added by the first of them; earlier entries are whatever the
// client sent. With fewer entries than hops, the first one.
func clientIP(r *http.Request, proxyHops int) string {
	if proxyHops > 0 {
		var entries []string
		for _, v := range r.Header.Values("X-Forwarded-For") {
			for e := range strings.SplitSeq(v, ",") {
				entries = append(entries, strings.TrimSpace(e))
			}
		}
		if len(entries) > 0 {
			if ip := net.ParseIP(entries[max(len(entries)-proxyHops, 0)]); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
