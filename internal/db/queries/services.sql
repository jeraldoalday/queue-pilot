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

-- name: ListBookableServicesByOrg :many
-- The services a customer may book themselves: active, and performed by
-- at least one staff member. A service nobody is assigned to generates no
-- slots at all, so listing it publicly offers a booking that can only end
-- in an empty page.
SELECT DISTINCT s.*
FROM services s
JOIN staff_services ss ON ss.service_id = s.id
JOIN users u ON u.id = ss.staff_user_id AND u.disabled_at IS NULL
WHERE s.org_id = $1 AND s.active
ORDER BY s.name;

-- name: UpdateService :one
UPDATE services
SET name = $3, description = $4, duration_minutes = $5, price_cents = $6, active = $7, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeleteService :exec
DELETE FROM services WHERE id = $1 AND org_id = $2;
