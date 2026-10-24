-- name: CreateTask :one
INSERT INTO tasks (column_id, title, description, position, created_by, priority, due_date, assignee_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1;

-- GetTaskDetail is GetTask plus the assignee's name, for API responses.
-- name: GetTaskDetail :one
SELECT sqlc.embed(t), u.name AS assignee_name
FROM tasks t
LEFT JOIN users u ON u.id = t.assignee_id
WHERE t.id = $1;

-- name: GetTaskForUpdate :one
SELECT * FROM tasks
WHERE id = $1
FOR UPDATE;

-- name: GetTaskWorkspaceID :one
SELECT b.workspace_id
FROM tasks t
JOIN columns c ON c.id = t.column_id
JOIN boards b ON b.id = c.board_id
WHERE t.id = $1;

-- name: ListTasksByColumn :many
SELECT * FROM tasks
WHERE column_id = $1
ORDER BY position, id;

-- ListTasksByBoard joins the assignee so the board loads in one query, not
-- one per task.
-- name: ListTasksByBoard :many
SELECT sqlc.embed(t), u.name AS assignee_name
FROM tasks t
JOIN columns c ON c.id = t.column_id
LEFT JOIN users u ON u.id = t.assignee_id
WHERE c.board_id = @board_id
  AND (@include_archived::boolean OR t.archived_at IS NULL)
ORDER BY t.position, t.id;

-- UpdateTask writes every editable field (the service merges the partial
-- edit first) and applies only if the caller saw the latest version. Zero
-- rows (pgx.ErrNoRows) means someone else changed it first: 409.
-- name: UpdateTask :one
UPDATE tasks
SET title       = $3,
    description = $4,
    priority    = $5,
    due_date    = $6,
    assignee_id = $7,
    archived_at = $8,
    version     = version + 1
WHERE id = $1 AND version = $2
RETURNING *;

-- MoveTask is UpdateTask for column and position: same version guard.
-- name: MoveTask :one
UPDATE tasks
SET column_id = $3,
    position  = $4,
    version   = version + 1
WHERE id = $1 AND version = $2
RETURNING *;

-- name: DeleteTask :execrows
DELETE FROM tasks
WHERE id = $1;

-- Neighbor lookups order by (position, id) so ties cannot hide a task.
-- They skip the task being moved.

-- name: LastTaskPosition :one
SELECT position FROM tasks
WHERE column_id = @column_id AND id <> @exclude_id
ORDER BY position DESC, id DESC
LIMIT 1;

-- name: TaskPositionAfter :one
SELECT position FROM tasks
WHERE column_id = @column_id
  AND id <> @exclude_id
  AND (position > @position OR (position = @position AND id > @id))
ORDER BY position, id
LIMIT 1;

-- name: TaskPositionBefore :one
SELECT position FROM tasks
WHERE column_id = @column_id
  AND id <> @exclude_id
  AND (position < @position OR (position = @position AND id < @id))
ORDER BY position DESC, id DESC
LIMIT 1;

-- RebalanceTasks spreads a column's tasks evenly (1024, 2048, ...) keeping
-- their order. The outer column_id check matters under READ COMMITTED: a task
-- moved out of the column concurrently must not get a position here.
-- name: RebalanceTasks :exec
UPDATE tasks t
SET position = (r.rn * 1024)::double precision
FROM (
    SELECT o.id, row_number() OVER (ORDER BY o.position, o.id) AS rn
    FROM tasks o
    WHERE o.column_id = $1
) r
WHERE t.id = r.id AND t.column_id = $1;
