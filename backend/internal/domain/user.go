package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MinPasswordLen = 8
	MaxPasswordLen = 128 // caps hashing cost per request
	MaxUserNameLen = 100
	MaxEmailLen    = 254
)

type User struct {
	ID           ID
	Email        string // lowercase
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

// Registration is a validated, normalized sign-up request.
type Registration struct {
	Email    string
	Name     string
	Password string
}

// NewRegistration normalizes email and name and checks the three fields.
// Lengths count characters, not bytes.
func NewRegistration(email, name, password string) (Registration, error) {
	r := Registration{Email: NormalizeEmail(email), Name: strings.TrimSpace(name), Password: password}

	fields := map[string]string{}
	if !validEmail(r.Email) {
		fields["email"] = "Introduce un correo válido"
	}
	if r.Name == "" {
		fields["name"] = "El nombre es obligatorio"
	} else if utf8.RuneCountInString(r.Name) > MaxUserNameLen {
		fields["name"] = fmt.Sprintf("El nombre no puede superar %d caracteres", MaxUserNameLen)
	}
	if n := utf8.RuneCountInString(password); n < MinPasswordLen {
		fields["password"] = fmt.Sprintf("La contraseña debe tener al menos %d caracteres", MinPasswordLen)
	} else if n > MaxPasswordLen {
		fields["password"] = fmt.Sprintf("La contraseña no puede superar %d caracteres", MaxPasswordLen)
	}
	if len(fields) > 0 {
		return Registration{}, &ValidationError{Fields: fields}
	}
	return r, nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail accepts a bare address (no display name) of a sane length.
func validEmail(email string) bool {
	if email == "" || len(email) > MaxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}
