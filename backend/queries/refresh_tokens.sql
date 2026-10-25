-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3);

-- name: RotateRefreshToken :one
-- Revokes a live token and stores its replacement in one statement, so two
-- concurrent refreshes with the same token cannot both succeed.
WITH old AS (
    UPDATE refresh_tokens AS t
    SET revoked_at = @now
    WHERE t.token_hash = @old_hash AND t.revoked_at IS NULL AND t.expires_at > @now
    RETURNING t.user_id
)
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
SELECT user_id, @new_hash, @expires_at FROM old
RETURNING user_id;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = @now
WHERE token_hash = @token_hash AND revoked_at IS NULL;

-- name: RevokeUserRefreshTokensOnReuse :execrows
-- If the token was revoked before @revoked_before, revokes every live token
-- of its user.
UPDATE refresh_tokens
SET revoked_at = @now
WHERE revoked_at IS NULL
  AND user_id = (
    SELECT r.user_id FROM refresh_tokens r
    WHERE r.token_hash = @token_hash AND r.revoked_at < @revoked_before
  );
