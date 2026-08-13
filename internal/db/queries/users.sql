-- name: CreateUser :one
INSERT INTO users (id, org_id, email, name, password_hash, role)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND org_id = $2;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);
