// Package domain holds Tablero's entities and pure business rules. It imports
// only the standard library: no database, HTTP or generated code.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// ID identifies an entity. It is a UUID; the layout matches github.com/google/uuid,
// so adapters convert between the two with a plain type conversion.
type ID [16]byte

// NilID is the zero ID; no entity has it.
var NilID ID

var errInvalidID = errors.New("invalid id")

// NewID returns a random (version 4) ID. The database generates the IDs of
// stored entities; this is for tests and in-memory implementations.
func NewID() ID {
	var id ID
	_, _ = rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id
}

// ParseID parses the canonical form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.
func ParseID(s string) (ID, error) {
	var id ID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return NilID, errInvalidID
	}
	hexOnly := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(id[:], []byte(hexOnly)); err != nil {
		return NilID, errInvalidID
	}
	return id, nil
}

func (id ID) IsZero() bool { return id == NilID }

// String returns the canonical lowercase form.
func (id ID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], id[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:], id[10:])
	return string(b[:])
}

// MarshalText makes IDs encode as JSON strings, like UUIDs.
func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

func (id *ID) UnmarshalText(b []byte) error {
	parsed, err := ParseID(string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
