-- name: CreateAvailability :one
INSERT INTO staff_availability (id, org_id, staff_user_id, weekday, start_time, end_time)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAvailabilityByStaff :many
SELECT * FROM staff_availability
WHERE org_id = $1 AND staff_user_id = $2
ORDER BY weekday, start_time;

-- name: DeleteAvailability :exec
DELETE FROM staff_availability WHERE id = $1 AND org_id = $2 AND staff_user_id = $3;
