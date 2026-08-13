package availability

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// ValidationError wraps a message that is always safe to show a user
// directly. See auth.ValidationError for the same pattern.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

type HoursInput struct {
	Weekday  int
	StartMin int
	EndMin   int
}

func (in HoursInput) validate() error {
	if in.Weekday < 0 || in.Weekday > 6 {
		return ValidationError{"weekday must be between 0 (Sunday) and 6 (Saturday)"}
	}
	if in.StartMin < 0 || in.StartMin >= 24*60 || in.EndMin < 0 || in.EndMin > 24*60 {
		return ValidationError{"start/end time must be within a single day"}
	}
	if in.EndMin <= in.StartMin {
		return ValidationError{"end time must be after start time"}
	}
	return nil
}

// AddHours records one recurring weekly window for staffUserID. orgID must
// come from the caller's authenticated tenant context.
func AddHours(ctx context.Context, pool *pgxpool.Pool, orgID, staffUserID string, in HoursInput) (sqlc.StaffAvailability, error) {
	if err := in.validate(); err != nil {
		return sqlc.StaffAvailability{}, err
	}

	return sqlc.New(pool).CreateAvailability(ctx, sqlc.CreateAvailabilityParams{
		ID:          db.MustUUID(id.New()),
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(staffUserID),
		Weekday:     int16(in.Weekday),
		StartTime:   db.TimeOfDayFromMinutes(in.StartMin),
		EndTime:     db.TimeOfDayFromMinutes(in.EndMin),
	})
}

func ListHours(ctx context.Context, pool *pgxpool.Pool, orgID, staffUserID string) ([]sqlc.StaffAvailability, error) {
	return sqlc.New(pool).ListAvailabilityByStaff(ctx, sqlc.ListAvailabilityByStaffParams{
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(staffUserID),
	})
}

func DeleteHours(ctx context.Context, pool *pgxpool.Pool, orgID, staffUserID, availabilityID string) error {
	return sqlc.New(pool).DeleteAvailability(ctx, sqlc.DeleteAvailabilityParams{
		ID:          db.MustUUID(availabilityID),
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(staffUserID),
	})
}

// ToWeeklyHours converts stored availability rows into the pure GenerateSlots
// input shape.
func ToWeeklyHours(rows []sqlc.StaffAvailability) []WeeklyHours {
	out := make([]WeeklyHours, len(rows))
	for i, r := range rows {
		out[i] = WeeklyHours{
			Weekday:  time.Weekday(r.Weekday),
			StartMin: db.MinutesOfDay(r.StartTime),
			EndMin:   db.MinutesOfDay(r.EndTime),
		}
	}
	return out
}
