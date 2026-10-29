package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	for name, want := range map[string]string{
		"Diseño de producto":    "diseno-de-producto",
		"  Equipo   Ñandú!! ":   "equipo-nandu",
		"Q4 · Lanzamiento":      "q4-lanzamiento",
		"¿?":                    "workspace",
		"AB":                    "ws-ab",
		strings.Repeat("a", 80): strings.Repeat("a", MaxSlugLen-slugRoom-1),
	} {
		got := Slugify(name)
		if got != want {
			t.Errorf("Slugify(%q) = %q, want %q", name, got, want)
		}
		if _, err := WorkspaceSlug(got); err != nil {
			t.Errorf("Slugify(%q) = %q is not a valid slug: %v", name, got, err)
		}
		if len(got)+slugRoom > MaxSlugLen+1 {
			t.Errorf("Slugify(%q) leaves no room for a suffix", name)
		}
	}
}

func TestWorkspaceSlug(t *testing.T) {
	for slug, ok := range map[string]bool{
		"equipo":                          true,
		" Equipo-2 ":                      true,
		"eq":                              false,
		"-equipo":                         false,
		"equipo-":                         false,
		"equi--po":                        false,
		"diseño":                          false,
		"con espacio":                     false,
		strings.Repeat("a", MaxSlugLen):   true,
		strings.Repeat("a", MaxSlugLen+1): false,
	} {
		if _, err := WorkspaceSlug(slug); (err == nil) != ok {
			t.Errorf("WorkspaceSlug(%q) err = %v, want ok = %v", slug, err, ok)
		}
	}
}

func TestInviteEmails(t *testing.T) {
	got, err := InviteEmails([]string{" Ana@X.com ", "", "ana@x.com", "beto@x.com"})
	if err != nil || strings.Join(got, ",") != "ana@x.com,beto@x.com" {
		t.Errorf("InviteEmails = %v, %v", got, err)
	}
	tooMany := make([]string, MaxInviteEmails+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("persona%d@x.com", i)
	}
	for name, emails := range map[string][]string{
		"none":     {" ", ""},
		"invalid":  {"ana@x.com", "julia@"},
		"too many": tooMany,
	} {
		if _, err := InviteEmails(emails); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
