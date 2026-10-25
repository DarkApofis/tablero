// Package token implements auth.TokenIssuer: HS256 access tokens (JWT) and
// opaque refresh tokens.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

const issuer = "tablero"

// Issuer signs and verifies short-lived HS256 access tokens.
type Issuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewIssuer(secret []byte, ttl time.Duration) *Issuer {
	return &Issuer{secret: secret, ttl: ttl, now: time.Now}
}

// Issue returns a signed access token for userID and its expiry.
func (t *Issuer) Issue(userID domain.ID) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(t.ttl)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	signed, err := token.SignedString(t.secret)
	return signed, exp, err
}

// Verify checks signature, algorithm, issuer and expiry, and returns the user id.
func (t *Issuer) Verify(tokenString string) (domain.ID, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(tokenString, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return domain.NilID, domain.ErrInvalidToken
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return domain.NilID, domain.ErrInvalidToken
	}
	return domain.ID(id), nil
}

// NewRefreshToken returns a random opaque token for the client and the
// SHA-256 hash to store. The plain value is never persisted.
func (t *Issuer) NewRefreshToken() (plain string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, HashRefreshToken(plain), nil
}

// HashRefreshToken is the hash NewRefreshToken returns for plain.
func (t *Issuer) HashRefreshToken(plain string) []byte { return HashRefreshToken(plain) }

func HashRefreshToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
