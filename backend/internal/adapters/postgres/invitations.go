package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/DarkApofis/tablero/backend/internal/adapters/postgres/db"
	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Invitations implements workspace.InvitationRepository on top of the
// workspaces repository (for the memberships it creates).
type Invitations struct{ *Workspaces }

func NewInvitations(ws *Workspaces) *Invitations { return &Invitations{ws} }

func (r *Invitations) UpsertInvitation(ctx context.Context, inv domain.Invitation, invitedBy domain.ID) (domain.Invitation, error) {
	row, err := r.q(ctx).UpsertInvitation(ctx, db.UpsertInvitationParams{
		WorkspaceID: uuid.UUID(inv.WorkspaceID), Email: inv.Email, Role: db.WorkspaceRole(inv.Role),
		InvitedBy: uuid.NullUUID{UUID: uuid.UUID(invitedBy), Valid: true}, ExpiresAt: inv.ExpiresAt,
	})
	return toInvitation(row), err
}

func (r *Invitations) ListInvitations(ctx context.Context, workspaceID domain.ID) ([]domain.Invitation, error) {
	rows, err := r.q(ctx).ListInvitations(ctx, uuid.UUID(workspaceID))
	return mapAll(rows, toInvitation), err
}

func (r *Invitations) RenewInvitation(ctx context.Context, workspaceID, invitationID domain.ID, expiresAt time.Time) (domain.Invitation, error) {
	row, err := r.q(ctx).RenewInvitation(ctx, db.RenewInvitationParams{
		ID: uuid.UUID(invitationID), WorkspaceID: uuid.UUID(workspaceID), ExpiresAt: expiresAt,
	})
	return toInvitation(row), notFound(err, domain.ErrInvitationNotFound)
}

func (r *Invitations) DeleteInvitation(ctx context.Context, workspaceID, invitationID domain.ID) error {
	n, err := r.q(ctx).DeleteInvitation(ctx, db.DeleteInvitationParams{ID: uuid.UUID(invitationID), WorkspaceID: uuid.UUID(workspaceID)})
	return deleted(n, err, domain.ErrInvitationNotFound)
}

func (r *Invitations) TakeInvitations(ctx context.Context, email string, now time.Time) ([]domain.Invitation, error) {
	rows, err := r.q(ctx).TakeInvitations(ctx, db.TakeInvitationsParams{Email: email, Now: now})
	return mapAll(rows, func(row db.TakeInvitationsRow) domain.Invitation {
		return domain.Invitation{WorkspaceID: domain.ID(row.WorkspaceID), Email: email, Role: domain.Role(row.Role)}
	}), err
}

func (r *Invitations) InviteLink(ctx context.Context, workspaceID domain.ID) (domain.InviteLink, error) {
	row, err := r.q(ctx).GetInviteLink(ctx, uuid.UUID(workspaceID))
	return toInviteLink(row), notFound(err, domain.ErrNotFound)
}

func (r *Invitations) SaveInviteLink(ctx context.Context, link domain.InviteLink, createdBy domain.ID) (domain.InviteLink, error) {
	row, err := r.q(ctx).UpsertInviteLink(ctx, db.UpsertInviteLinkParams{
		WorkspaceID: uuid.UUID(link.WorkspaceID), Token: link.Token, Role: db.WorkspaceRole(link.Role),
		CreatedBy: uuid.NullUUID{UUID: uuid.UUID(createdBy), Valid: true}, ExpiresAt: link.ExpiresAt,
	})
	return toInviteLink(row), err
}

// DeleteInviteLink is idempotent: turning off a link that is off is fine.
func (r *Invitations) DeleteInviteLink(ctx context.Context, workspaceID domain.ID) error {
	_, err := r.q(ctx).DeleteInviteLink(ctx, uuid.UUID(workspaceID))
	return err
}

func (r *Invitations) InviteLinkByToken(ctx context.Context, token string) (domain.InviteLinkInfo, error) {
	row, err := r.q(ctx).GetInviteLinkByToken(ctx, token)
	return domain.InviteLinkInfo{
		WorkspaceID: domain.ID(row.WorkspaceID), WorkspaceName: row.WorkspaceName,
		Role: domain.Role(row.Role), ExpiresAt: row.ExpiresAt,
	}, notFound(err, domain.ErrInviteLinkInvalid)
}

func toInvitation(i db.WorkspaceInvitation) domain.Invitation {
	return domain.Invitation{
		ID: domain.ID(i.ID), WorkspaceID: domain.ID(i.WorkspaceID), Email: i.Email, Role: domain.Role(i.Role),
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
	}
}

func toInviteLink(l db.WorkspaceInviteLink) domain.InviteLink {
	return domain.InviteLink{
		WorkspaceID: domain.ID(l.WorkspaceID), Token: l.Token, Role: domain.Role(l.Role),
		CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}
}
