-- name: CreateAppointment :one
INSERT INTO appointments (
    id, org_id, service_id, staff_user_id,
    customer_name, customer_email, customer_phone,
    starts_at, ends_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetAppointment :one
SELECT * FROM appointments WHERE id = $1 AND org_id = $2;

-- name: ListAppointmentsForStaffOnDay :many
SELECT * FROM appointments
WHERE org_id = sqlc.arg(org_id)
  AND staff_user_id = sqlc.arg(staff_user_id)
  AND status = 'booked'
  AND starts_at >= sqlc.arg(day_start)
  AND starts_at < sqlc.arg(day_end)
ORDER BY starts_at;

-- name: ListAppointmentsForOrgOnDay :many
SELECT
    a.id, a.org_id, a.service_id, a.staff_user_id,
    a.customer_name, a.customer_email, a.customer_phone,
    a.starts_at, a.ends_at, a.status, a.created_at, a.updated_at,
    s.name AS service_name,
    u.name AS staff_name
FROM appointments a
JOIN services s ON s.id = a.service_id
JOIN users u ON u.id = a.staff_user_id
WHERE a.org_id = sqlc.arg(org_id)
  AND a.starts_at >= sqlc.arg(day_start)
  AND a.starts_at < sqlc.arg(day_end)
ORDER BY a.starts_at;

-- name: CountOverlappingAppointments :one
-- Half-open overlap test: an existing booking conflicts only if it starts
-- before the new one ends AND ends after the new one starts, so bookings
-- that merely touch at a boundary don't count as conflicts. Params are
-- named explicitly because positional names sqlc infers here read
-- backwards (it names them after the compared column, not the argument).
SELECT count(*) FROM appointments
WHERE org_id = sqlc.arg(org_id)
  AND staff_user_id = sqlc.arg(staff_user_id)
  AND status = 'booked'
  AND starts_at < sqlc.arg(new_ends_at)
  AND ends_at > sqlc.arg(new_starts_at);

-- name: UpdateAppointmentStatus :one
-- from_status makes this a compare-and-swap, for the same reason
-- UpdateTicketStatus is one: two staff resolving the same appointment at
-- once would otherwise both read 'booked', both find their move legal,
-- and both write, so whichever request finished second would decide the
-- outcome — a cancellation could overwrite a completion. The loser now
-- updates no rows and is reported as a conflict.
UPDATE appointments
SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id)
  AND org_id = sqlc.arg(org_id)
  AND status = sqlc.arg(from_status)
RETURNING *;
