DROP INDEX IF EXISTS queue_tickets_board_idx;
CREATE INDEX queue_tickets_queue_status_number_idx
    ON queue_tickets (queue_id, status, number);
DROP INDEX IF EXISTS queue_tickets_queue_date_number_idx;
ALTER TABLE queue_tickets DROP COLUMN ticket_date;
