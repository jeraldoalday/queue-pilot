-- name: AssignStaffService :exec
INSERT INTO staff_services (org_id, staff_user_id, service_id)
VALUES ($1, $2, $3)
ON CONFLICT (staff_user_id, service_id) DO NOTHING;

-- name: UnassignStaffService :exec
DELETE FROM staff_services WHERE staff_user_id = $1 AND service_id = $2 AND org_id = $3;

-- name: ListStaffForService :many
SELECT u.id, u.name, u.email
FROM staff_services ss
JOIN users u ON u.id = ss.staff_user_id
WHERE ss.service_id = $1 AND ss.org_id = $2
ORDER BY u.name;

-- name: ListStaffServiceAssignmentsByOrg :many
SELECT staff_user_id, service_id FROM staff_services WHERE org_id = $1;

-- name: ListStaffUsersByOrg :many
SELECT id, name, email FROM users WHERE org_id = $1 AND role = 'staff' ORDER BY name;

-- name: IsStaffAssignedToService :one
SELECT EXISTS(
    SELECT 1 FROM staff_services
    WHERE staff_user_id = $1 AND service_id = $2 AND org_id = $3
);
