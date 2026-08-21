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

-- name: ListActiveStaffForService :many
-- The staff a public booking may be assigned to. It differs from
-- ListStaffForService by excluding suspended accounts: the admin page
-- shows every assignment so it can be edited, while offering a customer a
-- slot with someone who has left the business books an appointment nobody
-- will keep.
SELECT u.id, u.name, u.email
FROM staff_services ss
JOIN users u ON u.id = ss.staff_user_id
WHERE ss.service_id = $1 AND ss.org_id = $2 AND u.disabled_at IS NULL
ORDER BY u.name;

-- name: ListStaffServiceAssignmentsByOrg :many
SELECT staff_user_id, service_id FROM staff_services WHERE org_id = $1;

-- name: ListStaffUsersByOrg :many
-- disabled_at comes back with the row because the staff page has to show
-- which accounts are suspended: a list that renders a disabled account
-- identically to an active one invites an admin to keep waiting for a
-- login that will never work.
SELECT id, name, email, disabled_at FROM users WHERE org_id = $1 AND role = 'staff' ORDER BY name;

-- name: IsStaffAssignedToService :one
SELECT EXISTS(
    SELECT 1 FROM staff_services
    WHERE staff_user_id = $1 AND service_id = $2 AND org_id = $3
);
