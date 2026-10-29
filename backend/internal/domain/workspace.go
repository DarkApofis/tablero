package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxWorkspaceNameLen        = 100
	MaxWorkspaceDescriptionLen = 500
	MinSlugLen                 = 3
	MaxSlugLen                 = 40
)

// MaxCreatedWorkspaces is how many workspaces one user may create. Joining
// others (by default, or being added) does not count.
const MaxCreatedWorkspaces = 2

type Workspace struct {
	ID   ID
	Name string
	// Slug is the workspace's address, unique. Empty for one created by the
	// previous version during a deploy, until an admin gives it one.
	Slug        string
	Description string
	// DefaultRole is the role invitations offer first and a new invitation
	// link grants.
	DefaultRole Role
	// OwnerID is nil when the owner deleted their account, and for
	// workspaces made outside the app (the default workspace).
	OwnerID   *ID
	CreatedAt time.Time
}

// IsOwner reports whether userID owns the workspace.
func (w Workspace) IsOwner(userID ID) bool { return w.OwnerID != nil && *w.OwnerID == userID }

// MayTransfer reports whether userID, an admin of the workspace, may hand it
// over: its owner may, and any admin when it has none.
func (w Workspace) MayTransfer(userID ID) bool { return w.OwnerID == nil || *w.OwnerID == userID }

// WorkspaceSummary is a workspace as one of its members sees it in a list.
type WorkspaceSummary struct {
	Workspace
	Role        Role
	MemberCount int64
}

// WorkspaceDetail is a workspace as one of its members sees it on its own
// page, with what it holds.
type WorkspaceDetail struct {
	WorkspaceSummary
	BoardCount int64 // not archived
	TaskCount  int64
	// IsDefault: the workspace every new account joins, which cannot be deleted.
	IsDefault bool
}

// WorkspaceChanges are the settings to change; nil fields stay as they are.
type WorkspaceChanges struct {
	Name        *string
	Slug        *string
	Description *string
	DefaultRole *Role
}

type Member struct {
	UserID   ID
	Email    string
	Name     string
	Role     Role
	JoinedAt time.Time
}

// WorkspaceName trims and checks a workspace name.
func WorkspaceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Invalid("name", "El nombre es obligatorio")
	}
	if utf8.RuneCountInString(name) > MaxWorkspaceNameLen {
		return "", Invalid("name", fmt.Sprintf("El nombre no puede superar %d caracteres", MaxWorkspaceNameLen))
	}
	return name, nil
}

// WorkspaceDescription trims and checks a description, which may be empty.
func WorkspaceDescription(d string) (string, error) {
	d = strings.TrimSpace(d)
	if utf8.RuneCountInString(d) > MaxWorkspaceDescriptionLen {
		return "", Invalid("description", fmt.Sprintf("La descripción no puede superar %d caracteres", MaxWorkspaceDescriptionLen))
	}
	return d, nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// WorkspaceSlug lowercases, trims and checks an address: 3 to 40 lowercase
// letters and digits, words joined by single dashes.
func WorkspaceSlug(slug string) (string, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	switch {
	case len(slug) < MinSlugLen || len(slug) > MaxSlugLen:
		return "", Invalid("slug", fmt.Sprintf("La dirección debe tener entre %d y %d caracteres", MinSlugLen, MaxSlugLen))
	case !slugPattern.MatchString(slug):
		return "", Invalid("slug", "Usa solo minúsculas sin acentos, números y guiones entre palabras")
	}
	return slug, nil
}

var unaccent = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
)

// slugRoom keeps room in a generated address for a "-NN" suffix.
const slugRoom = 4

// Slugify makes an address out of a name: "Diseño de producto" becomes
// "diseno-de-producto". What is left is always a valid address: "workspace"
// when nothing usable is, padded when too short.
func Slugify(name string) string {
	s := unaccent.Replace(strings.ToLower(name))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			dash = true
			continue
		}
		if b.Len() >= MaxSlugLen-slugRoom-1 {
			break
		}
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
		dash = false
	}
	slug := b.String()
	switch {
	case slug == "":
		return "workspace"
	case len(slug) < MinSlugLen:
		return "ws-" + slug
	}
	return slug
}
