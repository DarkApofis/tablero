-- InsertActivity returns the entry so the use case can publish it as an
-- event once the transaction commits.
-- name: InsertActivity :one
INSERT INTO activity_log (workspace_id, board_id, actor_id, type, payload)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- ListBoardActivity returns entries older than the cursor id, newest first.
-- The first page passes the largest bigint as cursor.
-- name: ListBoardActivity :many
SELECT a.id, a.type, a.payload, a.created_at, a.actor_id, u.name AS actor_name
FROM activity_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.board_id = @board_id AND a.id < @before_id
ORDER BY a.id DESC
LIMIT @row_limit;
