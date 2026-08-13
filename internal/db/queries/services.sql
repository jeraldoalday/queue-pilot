-- name: CreateService :one
INSERT INTO services (id, org_id, slug, name, description, duration_minutes, price_cents, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListServicesByOrg :many
SELECT * FROM services WHERE org_id = $1 ORDER BY name;

-- name: GetService :one
SELECT * FROM services WHERE id = $1 AND org_id = $2;

-- name: GetServiceBySlug :one
SELECT * FROM services WHERE org_id = $1 AND slug = $2;

-- name: UpdateService :one
UPDATE services
SET name = $3, description = $4, duration_minutes = $5, price_cents = $6, active = $7, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeleteService :exec
DELETE FROM services WHERE id = $1 AND org_id = $2;
