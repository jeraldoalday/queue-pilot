package db

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// pgUniqueViolation is the Postgres error code for a unique_violation
// (23505), returned e.g. when a signup races on an org slug or user email.
const pgUniqueViolation = "23505"

// pgExclusionViolation is the Postgres error code for exclusion_violation
// (23P01), returned when an EXCLUDE constraint rejects a row — e.g. two
// overlapping appointments for the same staff member.
const pgExclusionViolation = "23P01"

// IsUniqueViolation reports whether err is a Postgres unique constraint
// violation, so callers can turn it into a friendly "already taken" error
// instead of a generic 500.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// IsExclusionViolation reports whether err is a Postgres EXCLUDE
// constraint violation, so a lost double-booking race reads as "that slot
// was just taken" rather than an internal error.
func IsExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgExclusionViolation
}

// MustUUID converts an application-generated ID (from internal/platform/id)
// into pgtype.UUID. It panics on a malformed string, which is a programmer
// error here — these values never come from outside the process.
func MustUUID(s string) pgtype.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic("db: MustUUID called with invalid UUID: " + err.Error())
	}
	return pgtype.UUID{Bytes: [16]byte(u), Valid: true}
}

// ParseUUID converts an externally supplied string (URL path segment, form
// field) into pgtype.UUID, returning an error the caller should turn into
// a 400/404 rather than a panic.
func ParseUUID(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: [16]byte(u), Valid: true}, nil
}

func UUIDString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}

func Text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func TextString(t pgtype.Text) string {
	return t.String
}

func Int4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func Timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func Time(t pgtype.Timestamptz) time.Time {
	return t.Time
}

// TimeOfDayFromMinutes converts minutes-since-midnight into a Postgres
// TIME value, for staff_availability.start_time/end_time.
func TimeOfDayFromMinutes(minutes int) pgtype.Time {
	return pgtype.Time{Microseconds: int64(minutes) * 60 * 1_000_000, Valid: true}
}

// MinutesOfDay converts a Postgres TIME value back to minutes-since-midnight.
func MinutesOfDay(t pgtype.Time) int {
	return int(t.Microseconds / (60 * 1_000_000))
}
