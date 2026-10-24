package token

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

var secret = []byte("test-secret-test-secret-test-secret!")

func TestTokenRoundTrip(t *testing.T) {
	issuer := NewIssuer(secret, 15*time.Minute)
	id := domain.NewID()

	token, exp, err := issuer.Issue(id)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d < 14*time.Minute || d > 15*time.Minute {
		t.Errorf("expiry in %v, want ~15m", d)
	}
	got, err := issuer.Verify(token)
	if err != nil || got != id {
		t.Errorf("Verify = %v, %v; want %v", got, err, id)
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	issuer := NewIssuer(secret, time.Minute)
	id := domain.NewID()

	expired, _, _ := NewIssuer(secret, -time.Minute).Issue(id)
	otherKey, _, _ := NewIssuer([]byte("another-secret-another-secret-123"), time.Minute).Issue(id)

	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer: "tablero", Subject: id.String(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	wrongIssuer, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "otro", Subject: id.String(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(secret)

	noExpiry, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "tablero", Subject: id.String(),
	}).SignedString(secret)

	cases := map[string]string{
		"expired":      expired,
		"other key":    otherKey,
		"alg none":     none,
		"wrong issuer": wrongIssuer,
		"no expiry":    noExpiry,
		"not a jwt":    "abc",
		"empty":        "",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := issuer.Verify(tok); err != domain.ErrInvalidToken {
				t.Errorf("Verify = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestRefreshTokenIsRandomAndHashed(t *testing.T) {
	issuer := NewIssuer(secret, time.Minute)
	a, hashA, err := issuer.NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := issuer.NewRefreshToken()
	if a == b {
		t.Error("two refresh tokens are equal")
	}
	if len(hashA) != 32 || string(hashA) == a {
		t.Errorf("hash = %x", hashA)
	}
	if string(HashRefreshToken(a)) != string(hashA) {
		t.Error("HashRefreshToken is not deterministic")
	}
}
