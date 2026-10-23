package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var secret = []byte("test-secret-test-secret-test-secret!")

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("supersecreta")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q", hash)
	}

	if ok, err := VerifyPassword("supersecreta", hash); !ok || err != nil {
		t.Errorf("correct password: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword("Supersecreta", hash); ok || err != nil {
		t.Errorf("wrong password: ok=%v err=%v", ok, err)
	}

	other, _ := HashPassword("supersecreta")
	if other == hash {
		t.Error("two hashes of the same password are equal; salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$2a$10$bcrypt", "$argon2id$v=19$m=x$salt$hash"} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("VerifyPassword(%q) returned no error", h)
		}
	}
}

func TestTokenRoundTrip(t *testing.T) {
	issuer := NewTokenIssuer(secret, 15*time.Minute)
	id := uuid.New()

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
	issuer := NewTokenIssuer(secret, time.Minute)
	id := uuid.New()

	expired, _, _ := NewTokenIssuer(secret, -time.Minute).Issue(id)
	otherKey, _, _ := NewTokenIssuer([]byte("another-secret-another-secret-123"), time.Minute).Issue(id)

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
			if _, err := issuer.Verify(tok); err == nil {
				t.Error("Verify accepted the token")
			}
		})
	}
}

func TestRefreshTokenIsRandomAndHashed(t *testing.T) {
	a, hashA, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewRefreshToken()
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
