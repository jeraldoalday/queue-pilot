-- name: CreateSession :one
INSERT INTO sessions (token, user_id, org_id, csrf_token, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE token = $1;

-- name: GetSessionWithUser :one
SELECT
    s.token        AS session_token,
    s.expires_at   AS session_expires_at,
    s.csrf_token   AS session_csrf_token,
    u.id           AS user_id,
    u.org_id       AS org_id,
    u.email        AS user_email,
    u.name         AS user_name,
    u.role         AS user_role,
    u.disabled_at  AS user_disabled_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token = $1;

-- name: DeleteExpiredSessions :execrows
-- Removes sessions whose expiry has passed. Nothing else deletes them:
-- Get treats an expired row as absent, so without this the table only ever
-- grows, one row per login, forever.
DELETE FROM sessions WHERE expires_at < now();

-- name: DeleteSessionsForUser :execrows
-- Revokes every session one user holds, in one statement.
--
-- Session lookup already refuses a disabled user's token, so this is not
-- what makes disabling take effect — it is what makes it take effect
-- *tidily*. A suspended account otherwise leaves live rows behind that
-- authenticate nobody, and an admin reading the sessions table cannot
-- tell a revoked login from a current one.
DELETE FROM sessions WHERE user_id = $1;
