-- name: CreateTask :one
INSERT INTO tasks (column_id, title, description, position, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1;

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

-- name: ListTasksByBoard :many
SELECT t.*
FROM tasks t
JOIN columns c ON c.id = t.column_id
WHERE c.board_id = $1
ORDER BY t.position, t.id;

-- UpdateTaskContent applies the change only if the caller saw the latest
-- version. Zero rows (pgx.ErrNoRows) means someone else changed it first: 409.
-- name: UpdateTaskContent :one
UPDATE tasks
SET title       = $3,
    description = $4,
    version     = version + 1
WHERE id = $1 AND version = $2
RETURNING *;

-- MoveTask is UpdateTaskContent for column and position: same version guard.
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
