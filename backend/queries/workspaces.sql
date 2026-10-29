-- name: CreateWorkspace :one
INSERT INTO workspaces (name, created_by)
VALUES ($1, $2)
RETURNING *;

-- name: CountWorkspacesCreatedBy :one
SELECT count(*) FROM workspaces WHERE created_by = $1;

-- LockWorkspaceCreator serializes, per user, creating workspaces until the
-- transaction ends, so two at once cannot both pass the limit.
-- name: LockWorkspaceCreator :exec
SELECT pg_advisory_xact_lock(hashtextextended('workspace_creator:' || CAST(sqlc.arg(user_id) AS uuid)::text, 0));

-- name: WorkspaceExists :one
SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = $1);

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
-- "at least one admin" check cannot race. NO KEY UPDATE, not UPDATE: it must
-- not block the KEY SHARE that foreign-key checks take on the workspace (an
-- activity_log insert, say), or a task edit holding a membership row lock
-- (LockMembership) and a member removal waiting for it would deadlock.
-- name: LockWorkspace :exec
SELECT id FROM workspaces
WHERE id = $1
FOR NO KEY UPDATE;

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

-- LockMembership blocks the membership row from being deleted until the
-- transaction ends (FOR KEY SHARE still lets the role change). Assigning a
-- task takes it so the assignee cannot be removed halfway through.
-- name: LockMembership :one
SELECT user_id FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2
FOR KEY SHARE;

-- LockMemberTasks locks the tasks UnassignMemberTasks is about to change and
-- returns their boards (one row per task), so the caller can take each
-- board's LockBoardActivity before the entries are written.
-- name: LockMemberTasks :many
SELECT c.board_id
FROM tasks t
JOIN columns c ON c.id = t.column_id
JOIN boards b ON b.id = c.board_id
WHERE b.workspace_id = @workspace_id::uuid AND t.assignee_id = @user_id::uuid
FOR UPDATE OF t;

-- UnassignMemberTasks runs when a member is removed: it unassigns their tasks
-- in the workspace, bumping each version as any edit does, and logs one
-- task_updated entry per task with the same payload shape the service writes
-- (domain.TaskUpdatedPayload). Run it after LockMemberTasks and the boards'
-- LockBoardActivity, in the same transaction.
-- name: UnassignMemberTasks :many
WITH unassigned AS (
    UPDATE tasks t
    SET assignee_id = NULL,
        version     = t.version + 1
    FROM columns c
    JOIN boards b ON b.id = c.board_id
    WHERE t.column_id = c.id
      AND b.workspace_id = @workspace_id::uuid
      AND t.assignee_id = @user_id::uuid
    RETURNING t.id, t.title, t.version, c.board_id
)
INSERT INTO activity_log (workspace_id, board_id, actor_id, type, payload)
SELECT @workspace_id::uuid, u.board_id, @actor_id::uuid, 'task_updated',
       jsonb_build_object(
           'task_id', u.id,
           'title', u.title,
           'version', u.version,
           'changes', jsonb_build_object(
               'assignee_id', jsonb_build_object('from', @user_id::uuid, 'to', NULL))
       )
FROM unassigned u
RETURNING *;
