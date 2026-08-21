-- name: CreateUser :one
INSERT INTO users (id, org_id, email, name, password_hash, role)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND org_id = $2;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: SetUserDisabled :one
-- Disables or re-enables one staff account.
--
-- The role filter is the safety rail: this statement can only ever touch
-- a row whose role is 'staff', so the admin route built on it cannot lock
-- an organisation out of its own account, not by an admin disabling
-- themselves, and not by one admin disabling another. Enforcing that here
-- rather than in the handler means every future caller inherits it.
UPDATE users
SET disabled_at = CASE WHEN sqlc.arg(disabled)::boolean THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND org_id = sqlc.arg(org_id)
  AND role = 'staff'
RETURNING *;
