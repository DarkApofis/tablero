-- LockBoardActivity serializes, per board, the last step of every write:
-- appending to the log. It is held until the transaction ends, so a board's
-- entries commit in id order, and "everything after id N" never misses an
-- entry that commits later with a smaller id. Take it last, right before
-- InsertActivity, and do nothing but insert and read after it.
-- name: LockBoardActivity :exec
SELECT pg_advisory_xact_lock(hashtextextended('activity_log:' || CAST(sqlc.arg(board_id) AS uuid)::text, 0));

-- InsertActivity derives the workspace from the board and returns the entry,
-- so the use case can publish it once the transaction commits. No row: the
-- board was deleted.
-- name: InsertActivity :one
INSERT INTO activity_log (workspace_id, board_id, actor_id, type, payload)
SELECT b.workspace_id, b.id, @actor_id, @type, @payload
FROM boards b
WHERE b.id = @board_id
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

-- ListBoardActivitySince returns the entries after after_id, oldest first:
-- what an event stream has not sent yet.
-- name: ListBoardActivitySince :many
SELECT * FROM activity_log
WHERE board_id = @board_id AND id > @after_id
ORDER BY id
LIMIT @row_limit;

-- name: LatestBoardActivityID :one
SELECT COALESCE(max(id), 0)::bigint FROM activity_log
WHERE board_id = $1;
