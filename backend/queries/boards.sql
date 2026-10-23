-- name: CreateBoard :one
INSERT INTO boards (workspace_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: ListBoardsByWorkspace :many
SELECT * FROM boards
WHERE workspace_id = $1
ORDER BY created_at, id;

-- name: GetBoard :one
SELECT * FROM boards
WHERE id = $1;

-- name: GetBoardWorkspaceID :one
SELECT workspace_id FROM boards
WHERE id = $1;

-- name: UpdateBoardName :one
UPDATE boards
SET name = $2
WHERE id = $1
RETURNING *;

-- Columns and tasks go with it (ON DELETE CASCADE).
-- name: DeleteBoard :execrows
DELETE FROM boards
WHERE id = $1;

-- LockBoard serializes position changes inside one board so two moves
-- cannot compute the same midpoint.
-- name: LockBoard :exec
SELECT id FROM boards
WHERE id = $1
FOR UPDATE;
