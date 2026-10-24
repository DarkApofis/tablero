package domain

import (
	"encoding/json"
	"testing"
)

func TestIDRoundTrip(t *testing.T) {
	id := NewID()
	if id.IsZero() || id[6]>>4 != 4 || id[8]>>6 != 2 {
		t.Errorf("NewID = %v, want a version 4 UUID", id)
	}
	parsed, err := ParseID(id.String())
	if err != nil || parsed != id {
		t.Errorf("ParseID(%s) = %v, %v", id, parsed, err)
	}

	b, _ := json.Marshal(struct {
		ID  ID  `json:"id"`
		Opt *ID `json:"opt"`
	}{ID: id})
	if string(b) != `{"id":"`+id.String()+`","opt":null}` {
		t.Errorf("json = %s", b)
	}
	var back struct{ ID ID }
	if err := json.Unmarshal([]byte(`{"ID":"`+id.String()+`"}`), &back); err != nil || back.ID != id {
		t.Errorf("unmarshal = %v, %v", back.ID, err)
	}
}

func TestParseIDCanonicalForm(t *testing.T) {
	const s = "485daec0-e2cf-435b-8f71-99ce04a8c742"
	id, err := ParseID("485DAEC0-E2CF-435B-8F71-99CE04A8C742")
	if err != nil || id.String() != s {
		t.Errorf("upper case: %v, %v", id, err)
	}
	for _, bad := range []string{"", "no-es-un-uuid", s[:35], s + "0", "485daec0e2cf435b8f7199ce04a8c742", "485daec0-e2cf-435b-8f71-99ce04a8c74g"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) accepted", bad)
		}
	}
}
