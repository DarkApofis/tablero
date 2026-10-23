-- name: CreateTask :one
INSERT INTO tasks (column_id, title, description, position, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1;

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

-- UpdateTask applies the change only if the caller saw the latest version.
-- Zero rows (pgx.ErrNoRows) means a concurrent update won: return 409.
-- name: UpdateTask :one
UPDATE tasks
SET column_id   = $3,
    title       = $4,
    description = $5,
    position    = $6,
    version     = version + 1
WHERE id = $1 AND version = $2
RETURNING *;
