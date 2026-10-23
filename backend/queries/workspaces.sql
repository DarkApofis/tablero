-- name: CreateWorkspace :one
INSERT INTO workspaces (name)
VALUES ($1)
RETURNING *;

-- name: AddWorkspaceMember :exec
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3);

-- name: ListWorkspacesForUser :many
SELECT w.id, w.name, w.created_at, m.role,
       (SELECT count(*) FROM workspace_members c WHERE c.workspace_id = w.id) AS member_count
FROM workspaces w
JOIN workspace_members m ON m.workspace_id = w.id
WHERE m.user_id = $1
ORDER BY w.created_at, w.id;

-- name: GetMemberRole :one
SELECT role FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: ListWorkspaceMembers :many
SELECT m.user_id, u.email, u.name, m.role, m.created_at
FROM workspace_members m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY m.created_at, u.email;

-- name: GetWorkspaceMember :one
SELECT m.user_id, u.email, u.name, m.role, m.created_at
FROM workspace_members m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1 AND m.user_id = $2;

-- LockWorkspace serializes membership changes of one workspace so the
-- "at least one admin" check cannot race.
-- name: LockWorkspace :exec
SELECT id FROM workspaces
WHERE id = $1
FOR UPDATE;

-- name: CountWorkspaceAdmins :one
SELECT count(*) FROM workspace_members
WHERE workspace_id = $1 AND role = 'admin';

-- name: UpdateMemberRole :execrows
UPDATE workspace_members
SET role = $3
WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteWorkspaceMember :execrows
DELETE FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;
