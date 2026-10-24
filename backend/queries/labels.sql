-- name: CreateLabel :one
INSERT INTO labels (board_id, name, color)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListLabelsByBoard :many
SELECT * FROM labels
WHERE board_id = $1
ORDER BY name, id;

-- name: GetLabelWorkspaceID :one
SELECT b.workspace_id
FROM labels l
JOIN boards b ON b.id = l.board_id
WHERE l.id = $1;

-- UpdateLabel changes only the fields given (NULL keeps the current value).
-- name: UpdateLabel :one
UPDATE labels
SET name  = COALESCE(sqlc.narg(name), name),
    color = COALESCE(sqlc.narg(color), color)
WHERE id = @id
RETURNING *;

-- Its task_labels rows go with it (ON DELETE CASCADE).
-- name: DeleteLabel :execrows
DELETE FROM labels
WHERE id = $1;

-- FilterBoardLabels returns which of ids are labels of the board, to reject
-- labels from other boards or that do not exist, and keeps them from being
-- deleted until the transaction ends, so assigning them cannot race with a
-- delete (FOR KEY SHARE: renaming them is still allowed).
-- name: FilterBoardLabels :many
SELECT id FROM labels
WHERE board_id = @board_id AND id = ANY(@ids::uuid[])
FOR KEY SHARE;

-- name: ListTaskLabels :many
SELECT l.*
FROM task_labels tl
JOIN labels l ON l.id = tl.label_id
WHERE tl.task_id = $1
ORDER BY l.name, l.id;

-- ListBoardTaskLabels returns the labels of every task of a board in one
-- query. Tasks only carry labels of their own board, so filtering by the
-- label's board is enough.
-- name: ListBoardTaskLabels :many
SELECT tl.task_id, sqlc.embed(l)
FROM task_labels tl
JOIN labels l ON l.id = tl.label_id
WHERE l.board_id = $1
ORDER BY l.name, l.id;

-- name: ClearTaskLabels :exec
DELETE FROM task_labels
WHERE task_id = $1;

-- name: AddTaskLabels :exec
INSERT INTO task_labels (task_id, label_id)
SELECT @task_id::uuid, unnest(@label_ids::uuid[]);
