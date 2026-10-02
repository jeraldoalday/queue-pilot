-- Prevent overlapping recurring availability windows at the database level.
--
-- AddHours already checks for an overlap (OverlapsWeeklyHours) before
-- inserting, but that check alone cannot close the race: two concurrent
-- requests adding hours for the same staff member can both read the
-- existing windows, both see no overlap, and both insert -- the exact
-- shape of race 000002 closed for appointment bookings with an EXCLUDE
-- constraint, because the application-level check runs outside any
-- transaction that would make it atomic with the insert.
--
-- Two overlapping windows don't corrupt anything by themselves, but
-- GenerateSlots has no notion of "the same slot twice": it walks each
-- window independently, so an overlap silently offers one time once per
-- window that covers it, which the booking form then renders as
-- duplicate, unlabelled buttons for what is really one slot (see
-- availability.OverlapsWeeklyHours).
--
-- btree_gist (enabled by 000002) supplies the equality operator classes
-- GiST needs for staff_user_id (uuid) and weekday (smallint). Postgres has
-- no built-in range type over TIME, so the window is expressed as an
-- int4range of minutes-since-midnight -- exact, since every stored
-- start_time/end_time is itself a whole number of minutes
-- (TimeOfDayFromMinutes never writes seconds). int4range's default bounds
-- are '[)', matching the half-open interval semantics
-- OverlapsWeeklyHours/GenerateSlots already use, so two windows that merely
-- touch at a boundary still do not count as overlapping here.
ALTER TABLE staff_availability
    ADD CONSTRAINT staff_availability_no_overlap
    EXCLUDE USING gist (
        staff_user_id WITH =,
        weekday WITH =,
        int4range(
            (EXTRACT(EPOCH FROM start_time - TIME '00:00') / 60)::int,
            (EXTRACT(EPOCH FROM end_time - TIME '00:00') / 60)::int
        ) WITH &&
    );
