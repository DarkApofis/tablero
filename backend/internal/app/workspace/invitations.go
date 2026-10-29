package workspace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// InvitationRepository stores invitations by email and invitation links.
type InvitationRepository interface {
	// UpsertInvitation creates the email's invitation to the workspace, or
	// replaces its role and expiry if it has one.
	UpsertInvitation(ctx context.Context, inv domain.Invitation, invitedBy domain.ID) (domain.Invitation, error)
	// ListInvitations returns the workspace's invitations, newest first,
	// expired ones included.
	ListInvitations(ctx context.Context, workspaceID domain.ID) ([]domain.Invitation, error)
	// RenewInvitation and DeleteInvitation return domain.ErrInvitationNotFound.
	RenewInvitation(ctx context.Context, workspaceID, invitationID domain.ID, expiresAt time.Time) (domain.Invitation, error)
	DeleteInvitation(ctx context.Context, workspaceID, invitationID domain.ID) error
	// TakeInvitations deletes and returns the email's invitations that are
	// still valid at now.
	TakeInvitations(ctx context.Context, email string, now time.Time) ([]domain.Invitation, error)

	// InviteLink returns domain.ErrNotFound if the workspace has none.
	InviteLink(ctx context.Context, workspaceID domain.ID) (domain.InviteLink, error)
	// SaveInviteLink replaces the workspace's link, if any.
	SaveInviteLink(ctx context.Context, link domain.InviteLink, createdBy domain.ID) (domain.InviteLink, error)
	DeleteInviteLink(ctx context.Context, workspaceID domain.ID) error
	// InviteLinkByToken returns domain.ErrInviteLinkInvalid for an unknown token.
	InviteLinkByToken(ctx context.Context, token string) (domain.InviteLinkInfo, error)

	// AddMemberIfAbsent adds userID with role unless they are a member
	// already (keeping their role); it reports whether it added them.
	AddMemberIfAbsent(ctx context.Context, workspaceID, userID domain.ID, role domain.Role) (bool, error)
	Member(ctx context.Context, workspaceID, userID domain.ID) (domain.Member, error)
}

// UserFinder finds accounts by email.
type UserFinder interface {
	// ByEmail returns domain.ErrUserNotFound.
	ByEmail(ctx context.Context, email string) (domain.User, error)
}

type Clock interface {
	Now() time.Time
}

// Invitations implements inviting people to a workspace: by email (whoever
// has an account joins at once; anyone else when they register with it) and
// by a link anyone holding it may use. No email is ever sent.
type Invitations struct {
	repo  InvitationRepository
	users UserFinder
	tx    TxManager
	clock Clock
}

func NewInvitations(repo InvitationRepository, users UserFinder, tx TxManager, clock Clock) *Invitations {
	return &Invitations{repo: repo, users: users, tx: tx, clock: clock}
}

func validRole(role domain.Role) error {
	if !role.Valid() {
		return domain.Invalid("role", "El rol debe ser admin, member o viewer")
	}
	return nil
}

// Invite invites emails to the workspace with role: people with an account
// become members now (members already keep their role), the rest get an
// invitation valid for domain.InvitationTTL that registering accepts.
func (s *Invitations) Invite(ctx context.Context, acc domain.Access, emails []string, role domain.Role) (domain.InviteResult, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return domain.InviteResult{}, err
	}
	emails, err := domain.InviteEmails(emails)
	if err != nil {
		return domain.InviteResult{}, err
	}
	if err := validRole(role); err != nil {
		return domain.InviteResult{}, err
	}

	var res domain.InviteResult
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		res = domain.InviteResult{Added: []domain.Member{}, Invited: []domain.Invitation{}, AlreadyMembers: []string{}}
		for _, email := range emails {
			user, err := s.users.ByEmail(ctx, email)
			if errors.Is(err, domain.ErrUserNotFound) {
				inv, err := s.repo.UpsertInvitation(ctx, domain.Invitation{
					WorkspaceID: acc.WorkspaceID, Email: email, Role: role,
					ExpiresAt: s.clock.Now().Add(domain.InvitationTTL),
				}, acc.UserID)
				if err != nil {
					return fmt.Errorf("save invitation: %w", err)
				}
				res.Invited = append(res.Invited, inv)
				continue
			}
			if err != nil {
				return fmt.Errorf("find user: %w", err)
			}
			added, err := s.repo.AddMemberIfAbsent(ctx, acc.WorkspaceID, user.ID, role)
			if err != nil {
				return fmt.Errorf("add member: %w", err)
			}
			if !added {
				res.AlreadyMembers = append(res.AlreadyMembers, email)
				continue
			}
			m, err := s.repo.Member(ctx, acc.WorkspaceID, user.ID)
			if err != nil {
				return fmt.Errorf("get member: %w", err)
			}
			res.Added = append(res.Added, m)
		}
		return nil
	})
	return res, err
}

func (s *Invitations) List(ctx context.Context, acc domain.Access) ([]domain.Invitation, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return nil, err
	}
	return s.repo.ListInvitations(ctx, acc.WorkspaceID)
}

// Renew gives an invitation domain.InvitationTTL more from now.
func (s *Invitations) Renew(ctx context.Context, acc domain.Access, invitationID domain.ID) (domain.Invitation, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return domain.Invitation{}, err
	}
	return s.repo.RenewInvitation(ctx, acc.WorkspaceID, invitationID, s.clock.Now().Add(domain.InvitationTTL))
}

func (s *Invitations) Cancel(ctx context.Context, acc domain.Access, invitationID domain.ID) error {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return err
	}
	return s.repo.DeleteInvitation(ctx, acc.WorkspaceID, invitationID)
}

// Link returns the workspace's invitation link, or nil if it has none.
// An expired one is returned too: the client says so.
func (s *Invitations) Link(ctx context.Context, acc domain.Access) (*domain.InviteLink, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return nil, err
	}
	link, err := s.repo.InviteLink(ctx, acc.WorkspaceID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// inviteTokenBytes is the entropy of a link token: 128 bits, unguessable.
const inviteTokenBytes = 16

// CreateLink makes a new invitation link with role, valid for
// domain.InvitationTTL. The previous link, if any, stops working.
func (s *Invitations) CreateLink(ctx context.Context, acc domain.Access, role domain.Role) (domain.InviteLink, error) {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return domain.InviteLink{}, err
	}
	if err := validRole(role); err != nil {
		return domain.InviteLink{}, err
	}
	raw := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return domain.InviteLink{}, fmt.Errorf("generate token: %w", err)
	}
	return s.repo.SaveInviteLink(ctx, domain.InviteLink{
		WorkspaceID: acc.WorkspaceID, Token: base64.RawURLEncoding.EncodeToString(raw), Role: role,
		ExpiresAt: s.clock.Now().Add(domain.InvitationTTL),
	}, acc.UserID)
}

// DeleteLink turns the workspace's invitation link off.
func (s *Invitations) DeleteLink(ctx context.Context, acc domain.Access) error {
	if err := acc.Require(domain.ActionManageMembers); err != nil {
		return err
	}
	return s.repo.DeleteInviteLink(ctx, acc.WorkspaceID)
}

// maxTokenLen bounds what is looked up as a token.
const maxTokenLen = 64

// LinkInfo tells someone holding a link where it leads, and whether they are
// a member there already. An unknown, replaced or expired link is
// domain.ErrInviteLinkInvalid.
func (s *Invitations) LinkInfo(ctx context.Context, userID domain.ID, token string) (domain.InviteLinkInfo, bool, error) {
	info, err := s.validLink(ctx, token)
	if err != nil {
		return domain.InviteLinkInfo{}, false, err
	}
	_, err = s.repo.Member(ctx, info.WorkspaceID, userID)
	if errors.Is(err, domain.ErrMemberNotFound) {
		return info, false, nil
	}
	return info, err == nil, err
}

// Join adds userID to the link's workspace with the link's role; a member
// already keeps their role. It returns the workspace's id.
func (s *Invitations) Join(ctx context.Context, userID domain.ID, token string) (domain.ID, error) {
	info, err := s.validLink(ctx, token)
	if err != nil {
		return domain.NilID, err
	}
	if _, err := s.repo.AddMemberIfAbsent(ctx, info.WorkspaceID, userID, info.Role); err != nil {
		return domain.NilID, fmt.Errorf("add member: %w", err)
	}
	return info.WorkspaceID, nil
}

func (s *Invitations) validLink(ctx context.Context, token string) (domain.InviteLinkInfo, error) {
	if token == "" || len(token) > maxTokenLen {
		return domain.InviteLinkInfo{}, domain.ErrInviteLinkInvalid
	}
	info, err := s.repo.InviteLinkByToken(ctx, token)
	if err != nil {
		return domain.InviteLinkInfo{}, err
	}
	if !s.clock.Now().Before(info.ExpiresAt) {
		return domain.InviteLinkInfo{}, domain.ErrInviteLinkInvalid
	}
	return info, nil
}

// Onboard accepts a new account's pending invitations: it becomes a member
// of each workspace that invited its email, with the role it was invited
// with. auth.Service.WithOnboarding runs it in the registration's transaction.
func (s *Invitations) Onboard(ctx context.Context, user domain.User) error {
	invitations, err := s.repo.TakeInvitations(ctx, user.Email, s.clock.Now())
	if err != nil {
		return fmt.Errorf("take invitations: %w", err)
	}
	for _, inv := range invitations {
		if _, err := s.repo.AddMemberIfAbsent(ctx, inv.WorkspaceID, user.ID, inv.Role); err != nil {
			return fmt.Errorf("accept invitation: %w", err)
		}
	}
	return nil
}
