package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxWorkspaceNameLen = 100

type Workspace struct {
	ID        ID
	Name      string
	CreatedAt time.Time
}

// WorkspaceSummary is a workspace as one of its members sees it in a list.
type WorkspaceSummary struct {
	Workspace
	Role        Role
	MemberCount int64
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
