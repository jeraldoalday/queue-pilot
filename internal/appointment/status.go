package appointment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// Status mirrors the appointments.status check constraint.
type Status string

const (
	StatusBooked    Status = "booked"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusNoShow    Status = "no_show"
)

var ErrInvalidTransition = errors.New("appointment: invalid status transition")

// allowedTransitions is the single source of truth for the appointment
// lifecycle. Keeping it as data (rather than scattered if-statements at
// call sites) means a handler cannot invent a transition the domain
// doesn't allow.
//
// The three end states are terminal: reopening a cancelled or completed
// appointment would let it silently reclaim a time slot someone else may
// have booked in the meantime, so it isn't permitted.
var allowedTransitions = map[Status][]Status{
	StatusBooked:    {StatusCompleted, StatusCancelled, StatusNoShow},
	StatusCompleted: {},
	StatusCancelled: {},
	StatusNoShow:    {},
}

func CanTransition(from, to Status) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// SetStatus moves an appointment to a new status, rejecting transitions
// the lifecycle doesn't allow. orgID must come from the caller's
// authenticated tenant context, so an appointment ID from another tenant
// resolves to "not found" rather than being mutated.
func SetStatus(ctx context.Context, pool *pgxpool.Pool, orgID, appointmentID string, to Status) (sqlc.Appointment, error) {
	q := sqlc.New(pool)

	current, err := q.GetAppointment(ctx, sqlc.GetAppointmentParams{
		ID:    db.MustUUID(appointmentID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Appointment{}, pgx.ErrNoRows
		}
		return sqlc.Appointment{}, err
	}

	if !CanTransition(Status(current.Status), to) {
		return sqlc.Appointment{}, ErrInvalidTransition
	}

	// Conditional on the status this read saw, so two staff resolving the
	// same appointment at once can't both succeed against one stale read
	// — a cancellation would otherwise be able to overwrite a completion
	// purely by finishing second.
	updated, err := q.UpdateAppointmentStatus(ctx, sqlc.UpdateAppointmentStatusParams{
		ID:         db.MustUUID(appointmentID),
		OrgID:      db.MustUUID(orgID),
		FromStatus: current.Status,
		Status:     string(to),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row was read successfully above, so no rows updated means
		// the status moved out from under this request.
		return sqlc.Appointment{}, ErrInvalidTransition
	}
	return updated, err
}

// ParseStatus converts a request-supplied status string into a Status,
// rejecting anything outside the known set so an arbitrary value can never
// reach the database.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusBooked, StatusCompleted, StatusCancelled, StatusNoShow:
		return Status(s), true
	default:
		return "", false
	}
}
