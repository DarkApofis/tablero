package domain

import (
	"fmt"
	"time"
)

const (
	// InvitationTTL is how long an invitation, by email or by link, is valid.
	InvitationTTL = 7 * 24 * time.Hour
	// MaxInviteEmails caps the emails of one invitation request.
	MaxInviteEmails = 20
)

// Invitation is an email without an account invited to a workspace. It is
// accepted when an account registers with that email before it expires; no
// email is sent.
type Invitation struct {
	ID          ID
	WorkspaceID ID
	Email       string
	Role        Role
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// InviteLink lets whoever has its token join its workspace with its role
// until it expires.
type InviteLink struct {
	WorkspaceID ID
	Token       string
	Role        Role
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// InviteLinkInfo is what someone holding a link sees before joining.
type InviteLinkInfo struct {
	WorkspaceID   ID
	WorkspaceName string
	Role          Role
	ExpiresAt     time.Time
}

// InviteResult is what inviting a list of emails did with each one.
type InviteResult struct {
	// Added: people who had an account, now members.
	Added []Member
	// Invited: emails without an account, now with a pending invitation.
	Invited []Invitation
	// AlreadyMembers: emails of members, whose role is left alone.
	AlreadyMembers []string
}

// InviteEmails normalizes and checks the emails to invite: at least one, at
// most MaxInviteEmails, each valid. Repeated ones count once.
func InviteEmails(emails []string) ([]string, error) {
	out := make([]string, 0, len(emails))
	seen := map[string]bool{}
	for _, e := range emails {
		e = NormalizeEmail(e)
		if e == "" || seen[e] {
			continue
		}
		if !validEmail(e) {
			return nil, Invalid("emails", fmt.Sprintf("«%s» no es un correo válido", e))
		}
		seen[e] = true
		out = append(out, e)
	}
	switch {
	case len(out) == 0:
		return nil, Invalid("emails", "Escribe al menos un correo")
	case len(out) > MaxInviteEmails:
		return nil, Invalid("emails", fmt.Sprintf("Como mucho %d correos a la vez", MaxInviteEmails))
	}
	return out, nil
}
