-- Demo data (cmd/seed and DEMO_MODE): see internal/demo. Everything else
-- goes through the use cases; these are what they have no reason to offer.

-- UpsertDemoUser creates the user or puts back the documented name and
-- password, so the demo credentials always work.
-- name: UpsertDemoUser :one
INSERT INTO users (email, name, password_hash)
VALUES ($1, $2, $3)
ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name, password_hash = EXCLUDED.password_hash
RETURNING *;

-- FindAdminWorkspace returns the oldest workspace called name that user_id administers.
-- name: FindAdminWorkspace :one
SELECT w.id FROM workspaces w
JOIN workspace_members m ON m.workspace_id = w.id
WHERE m.user_id = @user_id AND m.role = 'admin' AND w.name = @name
ORDER BY w.created_at, w.id
LIMIT 1;

-- name: UpsertWorkspaceMember :exec
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- FindBoardByName returns the oldest board called name in the workspace.
-- name: FindBoardByName :one
SELECT id FROM boards
WHERE workspace_id = $1 AND name = $2
ORDER BY created_at, id
LIMIT 1;

-- name: DeleteOtherBoards :exec
DELETE FROM boards WHERE workspace_id = $1 AND id <> $2;

-- ClearBoard deletes the board's columns (and with them its tasks), labels
-- and activity, keeping the board itself, so its id and URL survive.
-- name: ClearBoardColumns :exec
DELETE FROM columns WHERE board_id = $1;

-- name: ClearBoardLabels :exec
DELETE FROM labels WHERE board_id = $1;

-- name: ClearBoardActivity :exec
DELETE FROM activity_log WHERE board_id = $1;

-- SetActivityTimes gives the board's entries, in id order, the given times
-- (as if the scripted history had happened over the past days).
-- name: SetActivityTimes :exec
UPDATE activity_log a SET created_at = t.at
FROM (SELECT l.id, row_number() OVER (ORDER BY l.id) AS n FROM activity_log l WHERE l.board_id = @board_id::uuid) s
JOIN unnest(@times::timestamptz[]) WITH ORDINALITY AS t(at, n) ON t.n = s.n
WHERE a.id = s.id;

-- name: CountUsersByEmailSuffixSince :one
SELECT count(*) FROM users
WHERE email LIKE '%' || @suffix::text AND created_at >= @since;

-- DeleteUsersByEmailSuffixBefore deletes guest accounts: their memberships
-- and sessions go with them; their tasks and activity stay, unattributed.
-- name: DeleteUsersByEmailSuffixBefore :execrows
DELETE FROM users
WHERE email LIKE '%' || @suffix::text AND created_at < @before;

-- TryDemoLock takes a transaction-scoped lock so that, with several API
-- instances, only one restores the demo at a time.
-- name: TryDemoLock :one
SELECT pg_try_advisory_xact_lock(hashtextextended('tablero:demo', 0));
