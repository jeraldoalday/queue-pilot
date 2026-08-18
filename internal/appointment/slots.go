package appointment

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// AvailableSlots returns the bookable slots for one staff member, one
// service, on one date. It combines the staff member's recurring weekly
// hours with their already-booked appointments that day, then delegates
// the actual slot math to availability.GenerateSlots (pure, unit-tested).
//
// Both the admin manual-booking UI and the public booking page use this,
// so the two can't disagree about what's available.
func AvailableSlots(ctx context.Context, pool *pgxpool.Pool, orgID, staffUserID, serviceID string, date time.Time) ([]availability.Slot, error) {
	// staffUserID and serviceID come from form fields, so they are
	// validated rather than trusted: without this a malformed selection
	// reaches db.MustUUID and panics the request.
	if !db.IsUUID(serviceID) {
		return nil, ValidationError{"please choose a service"}
	}
	if !db.IsUUID(staffUserID) {
		return nil, ValidationError{"please choose a staff member"}
	}

	q := sqlc.New(pool)

	svc, err := q.GetService(ctx, sqlc.GetServiceParams{
		ID:    db.MustUUID(serviceID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return nil, err
	}

	hourRows, err := availability.ListHours(ctx, pool, orgID, staffUserID)
	if err != nil {
		return nil, err
	}

	dayStart := startOfDay(date)
	dayEnd := dayStart.AddDate(0, 0, 1)

	booked, err := q.ListAppointmentsForStaffOnDay(ctx, sqlc.ListAppointmentsForStaffOnDayParams{
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(staffUserID),
		DayStart:    db.Timestamptz(dayStart),
		DayEnd:      db.Timestamptz(dayEnd),
	})
	if err != nil {
		return nil, err
	}

	existing := make([]availability.Booking, len(booked))
	for i, b := range booked {
		existing[i] = availability.Booking{
			Start: db.Time(b.StartsAt),
			End:   db.Time(b.EndsAt),
		}
	}

	duration := time.Duration(svc.DurationMinutes) * time.Minute
	return availability.GenerateSlots(availability.ToWeeklyHours(hourRows), existing, dayStart, duration, 0), nil
}

// ListForOrgOnDay returns every appointment in the org on one date, joined
// with service and staff names for display.
func ListForOrgOnDay(ctx context.Context, pool *pgxpool.Pool, orgID string, date time.Time) ([]sqlc.ListAppointmentsForOrgOnDayRow, error) {
	dayStart := startOfDay(date)
	return sqlc.New(pool).ListAppointmentsForOrgOnDay(ctx, sqlc.ListAppointmentsForOrgOnDayParams{
		OrgID:    db.MustUUID(orgID),
		DayStart: db.Timestamptz(dayStart),
		DayEnd:   db.Timestamptz(dayStart.AddDate(0, 0, 1)),
	})
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
