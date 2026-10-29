-- Inviting an email again replaces its invitation: new role, new expiry.
-- name: UpsertInvitation :one
INSERT INTO workspace_invitations (workspace_id, email, role, invited_by, expires_at)
VALUES (@workspace_id, @email, @role, @invited_by, @expires_at)
ON CONFLICT (workspace_id, email) DO UPDATE
SET role = excluded.role, invited_by = excluded.invited_by, created_at = now(), expires_at = excluded.expires_at
RETURNING *;

-- name: ListInvitations :many
SELECT * FROM workspace_invitations
WHERE workspace_id = $1
ORDER BY created_at DESC, email;

-- name: RenewInvitation :one
UPDATE workspace_invitations
SET expires_at = @expires_at
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: DeleteInvitation :execrows
DELETE FROM workspace_invitations
WHERE id = $1 AND workspace_id = $2;

-- TakeInvitations deletes and returns the invitations of an email that have
-- not expired: a new account with that email accepts them. Expired ones stay
-- for their admins to renew or cancel.
-- name: TakeInvitations :many
DELETE FROM workspace_invitations
WHERE email = @email AND expires_at > @now
RETURNING workspace_id, role;

-- name: GetInviteLink :one
SELECT * FROM workspace_invite_links
WHERE workspace_id = $1;

-- A new link replaces the old one, which stops working.
-- name: UpsertInviteLink :one
INSERT INTO workspace_invite_links (workspace_id, token, role, created_by, expires_at)
VALUES (@workspace_id, @token, @role, @created_by, @expires_at)
ON CONFLICT (workspace_id) DO UPDATE
SET token = excluded.token, role = excluded.role, created_by = excluded.created_by,
    created_at = now(), expires_at = excluded.expires_at
RETURNING *;

-- name: DeleteInviteLink :execrows
DELETE FROM workspace_invite_links
WHERE workspace_id = $1;

-- name: GetInviteLinkByToken :one
SELECT l.workspace_id, l.role, l.expires_at, w.name AS workspace_name
FROM workspace_invite_links l
JOIN workspaces w ON w.id = l.workspace_id
WHERE l.token = $1;
