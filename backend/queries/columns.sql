-- name: CreateColumn :one
INSERT INTO columns (board_id, name, position)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListColumnsByBoard :many
SELECT * FROM columns
WHERE board_id = $1
ORDER BY position, id;

-- name: GetColumn :one
SELECT * FROM columns
WHERE id = $1;

-- LockColumn serializes position changes inside one column.
-- name: LockColumn :exec
SELECT id FROM columns
WHERE id = $1
FOR UPDATE;

-- name: GetColumnLocation :one
SELECT c.board_id, b.workspace_id
FROM columns c
JOIN boards b ON b.id = c.board_id
WHERE c.id = $1;

-- name: GetColumnWorkspaceID :one
SELECT b.workspace_id
FROM columns c
JOIN boards b ON b.id = c.board_id
WHERE c.id = $1;

-- name: UpdateColumnName :one
UPDATE columns
SET name = $2
WHERE id = $1
RETURNING *;

-- name: UpdateColumnPosition :one
UPDATE columns
SET position = $2
WHERE id = $1
RETURNING *;

-- name: ListColumnTaskIDs :many
SELECT id FROM tasks
WHERE column_id = $1
ORDER BY position, id;

-- Tasks go with it (ON DELETE CASCADE).
-- name: DeleteColumn :execrows
DELETE FROM columns
WHERE id = $1;

-- RebalanceColumns spreads a board's columns evenly (1024, 2048, ...) keeping
-- their order, for when repeated midpoints run out of float precision.
-- name: RebalanceColumns :exec
UPDATE columns c
SET position = (r.rn * 1024)::double precision
FROM (
    SELECT o.id, row_number() OVER (ORDER BY o.position, o.id) AS rn
    FROM columns o
    WHERE o.board_id = $1
) r
WHERE c.id = r.id;
