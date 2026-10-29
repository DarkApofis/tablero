-- name: CreateBoard :one
INSERT INTO boards (workspace_id, name, visibility, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetBoard :one
SELECT * FROM boards
WHERE id = $1;

-- name: GetBoardWorkspaceID :one
SELECT workspace_id, id AS board_id FROM boards
WHERE id = $1;

-- Columns and tasks go with it (ON DELETE CASCADE).
-- name: DeleteBoard :execrows
DELETE FROM boards
WHERE id = $1;

-- LockBoard serializes position changes inside one board so two moves
-- cannot compute the same midpoint. NO KEY UPDATE, not UPDATE: it must not
-- block the KEY SHARE that inserting into activity_log takes on the board
-- (a writer holding LockBoardActivity would wait for it, while this one
-- waits for that writer's column lock: a deadlock).
-- name: LockBoard :exec
SELECT id FROM boards
WHERE id = $1
FOR NO KEY UPDATE;

-- ListBoardSummaries lists the boards of a workspace the user may see (with
-- see_all, every one; otherwise those open to the workspace and the private
-- ones they were given), archived or not, with their task count and when
-- they last changed.
-- name: ListBoardSummaries :many
SELECT sqlc.embed(b),
       (SELECT count(*) FROM tasks t
        JOIN columns c ON c.id = t.column_id
        WHERE c.board_id = b.id AND t.archived_at IS NULL) AS task_count,
       COALESCE((SELECT a.created_at FROM activity_log a
                 WHERE a.board_id = b.id
                 ORDER BY a.id DESC
                 LIMIT 1), b.created_at)::timestamptz AS updated_at
FROM boards b
WHERE b.workspace_id = @workspace_id::uuid
  AND (b.archived_at IS NOT NULL) = @archived::boolean
  AND (@see_all::boolean
       OR b.visibility = 'workspace'
       OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id = b.id AND m.user_id = @user_id::uuid))
ORDER BY b.created_at, b.id;

-- ListBoardActors returns, per board, up to four people who changed it,
-- the most recent first.
-- name: ListBoardActors :many
SELECT x.board_id, x.user_id, x.name
FROM (
    SELECT a.board_id, a.actor_id AS user_id, u.name, max(a.id) AS last_id,
           row_number() OVER (PARTITION BY a.board_id ORDER BY max(a.id) DESC) AS rank
    FROM activity_log a
    JOIN users u ON u.id = a.actor_id
    WHERE a.board_id = ANY (@board_ids::uuid[])
    GROUP BY a.board_id, a.actor_id, u.name
) x
WHERE x.rank <= 4
ORDER BY x.board_id, x.last_id DESC;

-- name: UpdateBoard :one
UPDATE boards
SET name = @name, visibility = @visibility, archived_at = @archived_at
WHERE id = @id
RETURNING *;

-- GetBoardAccess tells whether the user may see the board when not an admin
-- of its workspace (it is open to the workspace, or private and they were
-- given it) and whether it is archived. The role is checked apart.
-- name: GetBoardAccess :one
SELECT (b.visibility = 'workspace'
        OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id = b.id AND m.user_id = @user_id::uuid))::boolean AS visible,
       (b.archived_at IS NOT NULL)::boolean AS archived
FROM boards b
WHERE b.id = @board_id::uuid;

-- name: ListBoardMembers :many
SELECT m.user_id, u.name, u.email
FROM board_members m
JOIN users u ON u.id = m.user_id
WHERE m.board_id = $1
ORDER BY u.name, u.email;

-- name: DeleteBoardMembers :exec
DELETE FROM board_members
WHERE board_id = $1;

-- AddBoardMember only adds members of the board's workspace: for anyone
-- else it affects no row.
-- name: AddBoardMember :execrows
INSERT INTO board_members (board_id, user_id)
SELECT b.id, m.user_id
FROM boards b
JOIN workspace_members m ON m.workspace_id = b.workspace_id AND m.user_id = @user_id::uuid
WHERE b.id = @board_id::uuid
ON CONFLICT DO NOTHING;

-- CopyBoardContent copies a board's columns, labels and the tasks that are
-- not archived (with their labels) into another, empty board. New ids are
-- drawn first so the copies can point at each other; each mapping CTE is
-- evaluated once (it calls a volatile function, so it is materialized).
-- name: CopyBoardContent :exec
WITH col_map AS (
    SELECT id AS old_id, gen_random_uuid() AS new_id
    FROM columns WHERE board_id = @source_id::uuid
), new_cols AS (
    INSERT INTO columns (id, board_id, name, position)
    SELECT m.new_id, @target_id::uuid, c.name, c.position
    FROM columns c JOIN col_map m ON m.old_id = c.id
), label_map AS (
    SELECT id AS old_id, gen_random_uuid() AS new_id
    FROM labels WHERE board_id = @source_id::uuid
), new_labels AS (
    INSERT INTO labels (id, board_id, name, color)
    SELECT m.new_id, @target_id::uuid, l.name, l.color
    FROM labels l JOIN label_map m ON m.old_id = l.id
), task_map AS (
    SELECT t.id AS old_id, gen_random_uuid() AS new_id
    FROM tasks t JOIN columns c ON c.id = t.column_id
    WHERE c.board_id = @source_id::uuid AND t.archived_at IS NULL
), new_tasks AS (
    INSERT INTO tasks (id, column_id, title, description, position, priority, due_date, assignee_id, created_by)
    SELECT tm.new_id, cm.new_id, t.title, t.description, t.position, t.priority, t.due_date, t.assignee_id, @actor_id::uuid
    FROM tasks t
    JOIN task_map tm ON tm.old_id = t.id
    JOIN col_map cm ON cm.old_id = t.column_id
)
INSERT INTO task_labels (task_id, label_id)
SELECT tm.new_id, lm.new_id
FROM task_labels tl
JOIN task_map tm ON tm.old_id = tl.task_id
JOIN label_map lm ON lm.old_id = tl.label_id;

-- name: CopyBoardMembers :exec
INSERT INTO board_members (board_id, user_id)
SELECT @target_id::uuid, user_id FROM board_members
WHERE board_id = @source_id::uuid;

-- MoveBoard puts the board in another workspace; MoveBoardActivity takes
-- its log along.
-- name: MoveBoard :one
UPDATE boards
SET workspace_id = @workspace_id::uuid
WHERE id = @id::uuid
RETURNING *;

-- name: MoveBoardActivity :exec
UPDATE activity_log
SET workspace_id = @workspace_id::uuid
WHERE board_id = @board_id::uuid;

-- DropBoardNonMembers takes off a moved private board whoever is not a
-- member of its new workspace.
-- name: DropBoardNonMembers :exec
DELETE FROM board_members bm
WHERE bm.board_id = @board_id::uuid
  AND NOT EXISTS (SELECT 1 FROM workspace_members m
                  WHERE m.workspace_id = @workspace_id::uuid AND m.user_id = bm.user_id);

-- LockBoardNonMemberTasks locks the tasks of a moved board assigned to
-- someone who is not a member of its new workspace, for
-- UnassignBoardNonMembers. Take it before LockBoardActivity.
-- name: LockBoardNonMemberTasks :many
SELECT t.id
FROM tasks t
JOIN columns c ON c.id = t.column_id
WHERE c.board_id = @board_id::uuid
  AND t.assignee_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM workspace_members m
                  WHERE m.workspace_id = @workspace_id::uuid AND m.user_id = t.assignee_id)
FOR UPDATE OF t;

-- UnassignBoardNonMembers unassigns them, bumping each version as any edit
-- does, and logs one task_updated entry per task, like UnassignMemberTasks.
-- name: UnassignBoardNonMembers :many
WITH targets AS (
    SELECT t.id, t.assignee_id
    FROM tasks t
    JOIN columns c ON c.id = t.column_id
    WHERE c.board_id = @board_id::uuid
      AND t.assignee_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM workspace_members m
                      WHERE m.workspace_id = @workspace_id::uuid AND m.user_id = t.assignee_id)
), unassigned AS (
    UPDATE tasks t
    SET assignee_id = NULL,
        version     = t.version + 1
    FROM targets x
    WHERE t.id = x.id
    RETURNING t.id, t.title, t.version, x.assignee_id AS old_assignee
)
INSERT INTO activity_log (workspace_id, board_id, actor_id, type, payload)
SELECT @workspace_id::uuid, @board_id::uuid, @actor_id::uuid, 'task_updated',
       jsonb_build_object(
           'task_id', u.id,
           'title', u.title,
           'version', u.version,
           'changes', jsonb_build_object(
               'assignee_id', jsonb_build_object('from', u.old_assignee, 'to', NULL))
       )
FROM unassigned u
RETURNING *;
