-- Prevent double-booking at the database level.
--
-- The application already checks for overlaps inside the booking
-- transaction, but that check alone cannot close the race: two concurrent
-- transactions can both pass it before either commits. This EXCLUDE
-- constraint is the actual guarantee — Postgres rejects the second insert
-- regardless of timing.
--
-- btree_gist is required because the constraint mixes an equality test on
-- a UUID (staff_user_id) with an overlap test on a range, and plain GiST
-- has no operator class for UUID equality.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Only 'booked' rows participate: a cancelled or no-show appointment must
-- not block that time from being rebooked.
ALTER TABLE appointments
    ADD CONSTRAINT appointments_no_overlap
    EXCLUDE USING gist (
        staff_user_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    )
    WHERE (status = 'booked');
