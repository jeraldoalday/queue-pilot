-- Anchor a queue ticket's number to the day it was issued.
--
-- queue_daily_counters already numbers tickets per (queue, date), but
-- queue_tickets itself only recorded created_at — an absolute instant.
-- Deriving "which service day is this ticket on" from created_at would
-- mean re-deriving the org's timezone on every read, and a queue that
-- runs past midnight local time would split one service day across two
-- UTC dates. Storing the org-local date the ticket was issued on makes
-- the day an explicit, indexable fact.
ALTER TABLE queue_tickets ADD COLUMN ticket_date DATE NOT NULL DEFAULT CURRENT_DATE;

-- The default exists only to backfill any rows written before this
-- migration; from here on the application supplies the org-local date.
ALTER TABLE queue_tickets ALTER COLUMN ticket_date DROP DEFAULT;

-- Two customers must never hold ticket #12 on the same day in the same
-- queue. The counter's atomic upsert already allocates distinct numbers,
-- but this makes it a guarantee the database enforces rather than a
-- property of one code path.
CREATE UNIQUE INDEX queue_tickets_queue_date_number_idx
    ON queue_tickets (queue_id, ticket_date, number);

-- Every board and console query filters by (queue, date) first and then
-- status, so the day-agnostic index the initial schema created is never
-- the right one. Replace it rather than keep both.
DROP INDEX queue_tickets_queue_status_number_idx;
CREATE INDEX queue_tickets_board_idx
    ON queue_tickets (queue_id, ticket_date, status, number);
