// Package appointment handles scheduled bookings: creating them against a
// staff member's availability, and moving them through their lifecycle.
package appointment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/emailshape"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// ErrSlotTaken means the requested time overlaps an existing booking for
// that staff member. Callers should re-render the slot list rather than
// treating it as an internal failure — it's an expected race outcome when
// two people book the same slot at once.
var ErrSlotTaken = errors.New("appointment: that time slot is no longer available")

// ErrStaffNotAssigned means the chosen staff member isn't assigned to the
// chosen service, which would otherwise let a caller book someone for work
// they don't perform.
var ErrStaffNotAssigned = errors.New("appointment: that staff member does not perform this service")

// ValidationError wraps a message that is always safe to show a user
// directly. See auth.ValidationError for the same pattern.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

// maxCustomerNameLen bounds a customer name the same way queue.TicketInput
// bounds one: so a pasted essay can't break the confirmation page or the
// admin booking form it's edited from. Deliberately generous.
const maxCustomerNameLen = 60

// maxCustomerEmailLen bounds the email an appointment is confirmed to.
// RFC 5321 caps an address at 254 characters; this is that bound, not a
// stylistic choice like the name and phone ones above it.
const maxCustomerEmailLen = 254

// maxCustomerPhoneLen bounds the optional callback number the same way
// queue.TicketInput bounds one.
const maxCustomerPhoneLen = 32

type BookInput struct {
	ServiceID     string
	StaffUserID   string
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
	StartsAt      time.Time
}

func (in BookInput) validate() error {
	// The IDs arrive as form fields, so a malformed value is ordinary bad
	// input rather than a programmer error — it has to be rejected here,
	// before db.MustUUID would panic on it further down.
	if !db.IsUUID(in.ServiceID) {
		return ValidationError{"please choose a service"}
	}
	if !db.IsUUID(in.StaffUserID) {
		return ValidationError{"please choose a staff member"}
	}
	name := strings.TrimSpace(in.CustomerName)
	if name == "" {
		return ValidationError{"customer name is required"}
	}
	if len(name) > maxCustomerNameLen {
		return ValidationError{fmt.Sprintf("customer name must be %d characters or fewer", maxCustomerNameLen)}
	}
	email := strings.TrimSpace(in.CustomerEmail)
	if email == "" {
		return ValidationError{"customer email is required"}
	}
	// The public booking path (BookPublic) already holds the customer's
	// email to this same loose shape check; Book is also reachable
	// directly from the admin console's own booking form, which skipped
	// it and would store whatever text was typed into the field as long
	// as it was non-empty and within the length bound.
	if !emailshape.LooksLikeEmail(email) {
		return ValidationError{"please enter a valid customer email"}
	}
	if len(email) > maxCustomerEmailLen {
		return ValidationError{fmt.Sprintf("customer email must be %d characters or fewer", maxCustomerEmailLen)}
	}
	if len(strings.TrimSpace(in.CustomerPhone)) > maxCustomerPhoneLen {
		return ValidationError{fmt.Sprintf("customer phone number must be %d characters or fewer", maxCustomerPhoneLen)}
	}
	if in.StartsAt.IsZero() {
		return ValidationError{"please choose a time slot"}
	}
	return nil
}

// Book creates an appointment for orgID. It is the single booking code
// path — the admin UI and the public booking page both call it, so the
// availability and double-booking rules can't drift between them.
//
// orgID must come from the caller's authenticated tenant context (admin
// UI) or the resolved org slug (public page), never from a client-supplied
// field.
//
// The overlap check and the insert run in one transaction: the check alone
// is not sufficient under concurrency (two requests can both pass it
// before either inserts), so a DB-level exclusion constraint added on
// Day 8 is the actual guarantee. This check exists to turn the common case
// into a friendly error rather than a constraint violation.
func Book(ctx context.Context, pool *pgxpool.Pool, orgID string, in BookInput) (sqlc.Appointment, error) {
	if err := in.validate(); err != nil {
		return sqlc.Appointment{}, err
	}

	q := sqlc.New(pool)

	svc, err := q.GetService(ctx, sqlc.GetServiceParams{
		ID:    db.MustUUID(in.ServiceID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Appointment{}, ValidationError{"that service no longer exists"}
		}
		return sqlc.Appointment{}, err
	}

	// The staff member must actually perform this service. This is scoped
	// by org_id too, so a guessed staff/service ID from another tenant
	// fails here rather than producing a cross-tenant booking.
	assigned, err := q.IsStaffAssignedToService(ctx, sqlc.IsStaffAssignedToServiceParams{
		StaffUserID: db.MustUUID(in.StaffUserID),
		ServiceID:   db.MustUUID(in.ServiceID),
		OrgID:       db.MustUUID(orgID),
	})
	if err != nil {
		return sqlc.Appointment{}, err
	}
	if !assigned {
		return sqlc.Appointment{}, ErrStaffNotAssigned
	}

	endsAt := in.StartsAt.Add(time.Duration(svc.DurationMinutes) * time.Minute)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return sqlc.Appointment{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	qtx := sqlc.New(tx)

	overlaps, err := qtx.CountOverlappingAppointments(ctx, sqlc.CountOverlappingAppointmentsParams{
		OrgID:       db.MustUUID(orgID),
		StaffUserID: db.MustUUID(in.StaffUserID),
		NewStartsAt: db.Timestamptz(in.StartsAt),
		NewEndsAt:   db.Timestamptz(endsAt),
	})
	if err != nil {
		return sqlc.Appointment{}, err
	}
	if overlaps > 0 {
		return sqlc.Appointment{}, ErrSlotTaken
	}

	appt, err := qtx.CreateAppointment(ctx, sqlc.CreateAppointmentParams{
		ID:            db.MustUUID(id.New()),
		OrgID:         db.MustUUID(orgID),
		ServiceID:     db.MustUUID(in.ServiceID),
		StaffUserID:   db.MustUUID(in.StaffUserID),
		CustomerName:  strings.TrimSpace(in.CustomerName),
		CustomerEmail: strings.TrimSpace(in.CustomerEmail),
		CustomerPhone: db.Text(strings.TrimSpace(in.CustomerPhone)),
		StartsAt:      db.Timestamptz(in.StartsAt),
		EndsAt:        db.Timestamptz(endsAt),
	})
	if err != nil {
		// Once the Day 8 exclusion constraint exists, a concurrent booking
		// that beat this transaction surfaces here rather than in the
		// count check above.
		if db.IsExclusionViolation(err) {
			return sqlc.Appointment{}, ErrSlotTaken
		}
		return sqlc.Appointment{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return sqlc.Appointment{}, err
	}
	return appt, nil
}
