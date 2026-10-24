package hasher

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	var h Argon2id
	hash, err := h.Hash("supersecreta")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q", hash)
	}

	if ok, err := h.Verify("supersecreta", hash); !ok || err != nil {
		t.Errorf("correct password: ok=%v err=%v", ok, err)
	}
	if ok, err := h.Verify("Supersecreta", hash); ok || err != nil {
		t.Errorf("wrong password: ok=%v err=%v", ok, err)
	}

	other, _ := h.Hash("supersecreta")
	if other == hash {
		t.Error("two hashes of the same password are equal; salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{"", "plain", "$2a$10$bcrypt", "$argon2id$v=19$m=x$salt$hash"} {
		if _, err := (Argon2id{}).Verify("x", hash); err == nil {
			t.Errorf("Verify(%q) returned no error", hash)
		}
	}
}
