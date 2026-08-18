-- name: CreateQueue :one
INSERT INTO queues (id, org_id, slug, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListQueuesByOrg :many
SELECT * FROM queues WHERE org_id = $1 ORDER BY name;

-- name: ListActiveQueuesByOrg :many
SELECT * FROM queues WHERE org_id = $1 AND active ORDER BY name;

-- name: GetQueue :one
SELECT * FROM queues WHERE id = $1 AND org_id = $2;

-- name: GetQueueBySlug :one
SELECT * FROM queues WHERE org_id = $1 AND slug = $2;

-- name: UpdateQueue :one
UPDATE queues
SET name = $3, active = $4, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeleteQueue :exec
DELETE FROM queues WHERE id = $1 AND org_id = $2;
