-- name: NextTicketNumber :one
-- Allocates the next ticket number for one queue on one service day.
-- The upsert is a single statement, so two concurrent walk-ins can never
-- receive the same number: the second one blocks on the first's row lock
-- and then reads the already-incremented value.
INSERT INTO queue_daily_counters (queue_id, ticket_date, last_number)
VALUES (sqlc.arg(queue_id), sqlc.arg(ticket_date), 1)
ON CONFLICT (queue_id, ticket_date)
DO UPDATE SET last_number = queue_daily_counters.last_number + 1
RETURNING last_number;

-- name: CreateTicket :one
INSERT INTO queue_tickets (
    id, org_id, queue_id, ticket_date, number, customer_name, customer_phone
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTicket :one
SELECT * FROM queue_tickets WHERE id = $1 AND org_id = $2;

-- name: GetTicketInQueue :one
-- The public status page has no session to scope by, so it scopes by the
-- queue resolved from the URL instead: a ticket ID from another queue (or
-- another org's queue) reads as not found.
SELECT * FROM queue_tickets WHERE id = $1 AND queue_id = $2;

-- name: ListTicketsForQueueOnDate :many
SELECT * FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id) AND ticket_date = sqlc.arg(ticket_date)
ORDER BY number;

-- name: ListOpenTicketsForQueueOnDate :many
-- "Open" is everything still in play: waiting, called, or being served.
-- The board and the console both show exactly this set.
SELECT * FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id)
  AND ticket_date = sqlc.arg(ticket_date)
  AND status IN ('waiting', 'called', 'serving')
ORDER BY number;

-- name: LockNextWaitingTicket :one
-- Claims the lowest-numbered waiting ticket for the caller's transaction.
-- FOR UPDATE SKIP LOCKED is what makes "call next" safe with several staff
-- clicking at once: a row another transaction has already claimed is
-- skipped rather than waited on, so two staff always get two different
-- customers instead of both being handed the same one.
SELECT * FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id)
  AND ticket_date = sqlc.arg(ticket_date)
  AND status = 'waiting'
ORDER BY number
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: UpdateTicketStatus :one
-- Stamps the timestamp that matches the new status, so the lifecycle
-- times are recorded by the same statement that moves the ticket and
-- can't drift apart. called_by_user_id is only overwritten when a caller
-- is supplied, so a later transition doesn't erase who called the ticket.
UPDATE queue_tickets
SET status = sqlc.arg(status),
    called_by_user_id = COALESCE(sqlc.narg(called_by_user_id)::uuid, called_by_user_id),
    called_at = CASE WHEN sqlc.arg(status) = 'called' THEN now() ELSE called_at END,
    serving_at = CASE WHEN sqlc.arg(status) = 'serving' THEN now() ELSE serving_at END,
    completed_at = CASE
        WHEN sqlc.arg(status) IN ('done', 'no_show', 'cancelled') THEN now()
        ELSE completed_at
    END
WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id)
RETURNING *;

-- name: CountWaitingAhead :one
-- How many people are still waiting in front of ticket `number` — the
-- input to the customer-facing wait estimate.
SELECT count(*) FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id)
  AND ticket_date = sqlc.arg(ticket_date)
  AND status = 'waiting'
  AND number < sqlc.arg(number);

-- name: QueueStatsForDate :one
SELECT
    count(*) FILTER (WHERE status = 'waiting')   AS waiting,
    count(*) FILTER (WHERE status = 'called')    AS called,
    count(*) FILTER (WHERE status = 'serving')   AS serving,
    count(*) FILTER (WHERE status = 'done')      AS done,
    count(*) FILTER (WHERE status = 'no_show')   AS no_show,
    count(*) FILTER (WHERE status = 'cancelled') AS cancelled
FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id) AND ticket_date = sqlc.arg(ticket_date);

-- name: AverageServiceSeconds :one
-- Mean hands-on service time for recently completed tickets, used to
-- estimate waits. Only 'done' tickets count: a no-show or cancellation
-- says nothing about how long serving someone takes. The 30-day window
-- keeps the estimate responsive to how the business runs now.
SELECT COALESCE(
    avg(EXTRACT(EPOCH FROM (completed_at - serving_at))),
    0
)::float8 AS avg_seconds
FROM queue_tickets
WHERE queue_id = sqlc.arg(queue_id)
  AND status = 'done'
  AND serving_at IS NOT NULL
  AND completed_at IS NOT NULL
  AND completed_at > now() - interval '30 days';
