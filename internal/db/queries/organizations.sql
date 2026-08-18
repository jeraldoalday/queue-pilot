-- name: CreateOrganization :one
INSERT INTO organizations (id, name, slug, timezone)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations WHERE slug = $1;

-- name: CountOrganizations :one
SELECT count(*) FROM organizations;

-- name: UpdateOrganization :one
-- The slug is deliberately not updatable. It is printed on QR codes stuck
-- to a counter and bookmarked by customers, so a business fixing a typo in
-- its name must not break every board URL it has handed out.
UPDATE organizations
SET name = sqlc.arg(name),
    timezone = sqlc.arg(timezone),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;
