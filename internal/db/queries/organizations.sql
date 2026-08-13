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
