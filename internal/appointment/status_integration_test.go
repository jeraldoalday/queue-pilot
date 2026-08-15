//go:build integration

package appointment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// TestIntegrationDBConstraintBlocksOverlap is the proof that double-booking
// is prevented by the database, not merely by application logic. It inserts
// a conflicting row with raw SQL, bypassing appointment.Book and all its
// checks entirely — if this insert ever succeeds, the guarantee is gone
// even though every app-level test would still pass.
func TestIntegrationDBConstraintBlocksOverlap(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	start := nextMonday().Add(9 * time.Hour)
	end := start.Add(30 * time.Minute)

	if _, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "First Booker",
		CustomerEmail: "first@example.test",
		StartsAt:      start,
	}); err != nil {
		t.Fatalf("initial booking: %v", err)
	}

	// Overlaps the existing booking by 15 minutes, same staff member.
	_, err := f.pool.Exec(ctx, `
		INSERT INTO appointments
			(id, org_id, service_id, staff_user_id, customer_name, customer_email, starts_at, ends_at, status)
		VALUES ($1, $2, $3, $4, 'Raw Insert', 'raw@example.test', $5, $6, 'booked')`,
		db.MustUUID(id.New()), db.MustUUID(f.orgID), db.MustUUID(f.serviceID), db.MustUUID(f.staffID),
		start.Add(15*time.Minute), end.Add(15*time.Minute))

	if err == nil {
		t.Fatal("raw overlapping insert succeeded; the EXCLUDE constraint is not protecting against double-booking")
	}
	if !db.IsExclusionViolation(err) {
		t.Fatalf("insert failed with %v, want an exclusion_violation (23P01)", err)
	}
}

// TestIntegrationCancelledSlotIsRebookable confirms the constraint's
// WHERE (status = 'booked') clause works: cancelling frees the time.
func TestIntegrationCancelledSlotIsRebookable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	start := nextMonday().Add(10 * time.Hour)

	first, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "First Booker",
		CustomerEmail: "first@example.test",
		StartsAt:      start,
	})
	if err != nil {
		t.Fatalf("initial booking: %v", err)
	}

	// Same slot must be refused while the first booking stands.
	if _, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "Second Booker",
		CustomerEmail: "second@example.test",
		StartsAt:      start,
	}); !errors.Is(err, ErrSlotTaken) {
		t.Fatalf("rebooking a live slot gave %v, want ErrSlotTaken", err)
	}

	if _, err := SetStatus(ctx, f.pool, f.orgID, db.UUIDString(first.ID), StatusCancelled); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Now the freed time should be bookable again.
	if _, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "Second Booker",
		CustomerEmail: "second@example.test",
		StartsAt:      start,
	}); err != nil {
		t.Fatalf("rebooking a cancelled slot failed: %v", err)
	}
}

func TestIntegrationSetStatusRejectsTerminalTransition(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	appt, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "Dana Customer",
		CustomerEmail: "dana@example.test",
		StartsAt:      nextMonday().Add(11 * time.Hour),
	})
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	apptID := db.UUIDString(appt.ID)

	if _, err := SetStatus(ctx, f.pool, f.orgID, apptID, StatusCompleted); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := SetStatus(ctx, f.pool, f.orgID, apptID, StatusCancelled); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cancelling a completed appointment gave %v, want ErrInvalidTransition", err)
	}
}

// TestIntegrationSetStatusIsTenantScoped proves org A cannot mutate org B's
// appointment even with a valid appointment ID.
func TestIntegrationSetStatusIsTenantScoped(t *testing.T) {
	orgA := newFixture(t)
	orgB := newFixture(t)
	ctx := context.Background()

	apptB, err := Book(ctx, orgB.pool, orgB.orgID, BookInput{
		ServiceID:     orgB.serviceID,
		StaffUserID:   orgB.staffID,
		CustomerName:  "Org B Customer",
		CustomerEmail: "orgb@example.test",
		StartsAt:      nextMonday().Add(14 * time.Hour),
	})
	if err != nil {
		t.Fatalf("book in org B: %v", err)
	}

	_, err = SetStatus(ctx, orgA.pool, orgA.orgID, db.UUIDString(apptB.ID), StatusCancelled)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("org A cancelling org B's appointment gave %v, want pgx.ErrNoRows", err)
	}

	// And org B's appointment must be untouched.
	still, err := SetStatus(ctx, orgB.pool, orgB.orgID, db.UUIDString(apptB.ID), StatusCompleted)
	if err != nil {
		t.Fatalf("org B completing its own appointment failed: %v", err)
	}
	if still.Status != string(StatusCompleted) {
		t.Fatalf("status = %q, want completed", still.Status)
	}
}
