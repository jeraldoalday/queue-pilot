//go:build integration

package appointment

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// fixture is a fully set-up org: an admin, one staff member assigned to
// one service, with Monday 09:00-17:00 availability.
type fixture struct {
	pool      *pgxpool.Pool
	orgID     string
	staffID   string
	serviceID string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; run via `make test-integration`")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	sessions := session.NewStore(pool, clock.Real{})
	ident, err := auth.Signup(ctx, pool, sessions, auth.SignupInput{
		OrgName:    "Booking Test Org " + id.New(),
		AdminName:  "Test Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	staff, err := auth.CreateStaffUser(ctx, pool, ident.OrgID, auth.CreateStaffInput{
		Name:     "Test Staff",
		Email:    "staff-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffID := db.UUIDString(staff.ID)

	svc, err := catalog.CreateService(ctx, pool, ident.OrgID, catalog.ServiceInput{
		Name:            "Test Service " + id.New(),
		DurationMinutes: 30,
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	serviceID := db.UUIDString(svc.ID)

	if err := catalog.ToggleStaffAssignment(ctx, pool, ident.OrgID, serviceID, staffID); err != nil {
		t.Fatalf("assign staff to service: %v", err)
	}

	if _, err := availability.AddHours(ctx, pool, ident.OrgID, staffID, availability.HoursInput{
		Weekday:  int(time.Monday),
		StartMin: 9 * 60,
		EndMin:   17 * 60,
	}); err != nil {
		t.Fatalf("add availability: %v", err)
	}

	return fixture{pool: pool, orgID: ident.OrgID, staffID: staffID, serviceID: serviceID}
}

// nextMonday returns a Monday well in the future, so tests never collide
// with rows left by earlier runs on the same shared database.
func nextMonday() time.Time {
	t := time.Now().UTC().AddDate(0, 0, 7)
	for t.Weekday() != time.Monday {
		t = t.AddDate(0, 0, 1)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func TestIntegrationBookAndSlotDisappears(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := nextMonday()

	before, err := AvailableSlots(ctx, f.pool, f.orgID, f.staffID, f.serviceID, day)
	if err != nil {
		t.Fatalf("available slots: %v", err)
	}
	// 09:00-17:00 in 30-minute steps = 16 slots.
	if len(before) != 16 {
		t.Fatalf("got %d slots before booking, want 16", len(before))
	}

	target := before[0].Start
	if _, err := Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   f.staffID,
		CustomerName:  "Dana Customer",
		CustomerEmail: "dana@example.test",
		StartsAt:      target,
	}); err != nil {
		t.Fatalf("book: %v", err)
	}

	after, err := AvailableSlots(ctx, f.pool, f.orgID, f.staffID, f.serviceID, day)
	if err != nil {
		t.Fatalf("available slots after booking: %v", err)
	}
	if len(after) != len(before)-1 {
		t.Fatalf("got %d slots after booking, want %d", len(after), len(before)-1)
	}
	for _, s := range after {
		if s.Start.Equal(target) {
			t.Fatalf("booked slot %v is still offered as available", target)
		}
	}
}

// TestIntegrationConcurrentBookingsOnlyOneWins is the key safety test for
// the booking path: two goroutines race to book the identical slot, and
// exactly one must succeed. Today the app-level check inside a transaction
// wins most races; Day 8 adds a DB exclusion constraint so the guarantee
// no longer depends on timing at all.
func TestIntegrationConcurrentBookingsOnlyOneWins(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := nextMonday()

	slots, err := AvailableSlots(ctx, f.pool, f.orgID, f.staffID, f.serviceID, day)
	if err != nil {
		t.Fatalf("available slots: %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("no slots available to contend for")
	}
	target := slots[0].Start

	const attempts = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		okCount int
		errs    []error
	)

	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			_, err := Book(ctx, f.pool, f.orgID, BookInput{
				ServiceID:     f.serviceID,
				StaffUserID:   f.staffID,
				CustomerName:  "Racer",
				CustomerEmail: "racer@example.test",
				StartsAt:      target,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okCount++
			} else {
				errs = append(errs, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if okCount != 1 {
		t.Fatalf("got %d successful bookings for the same slot, want exactly 1 (errors: %v)", okCount, errs)
	}
	for _, err := range errs {
		if !errors.Is(err, ErrSlotTaken) {
			t.Errorf("losing booking failed with %v, want ErrSlotTaken", err)
		}
	}
}

func TestIntegrationBookRejectsUnassignedStaff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A second staff member who is NOT assigned to the service.
	other, err := auth.CreateStaffUser(ctx, f.pool, f.orgID, auth.CreateStaffInput{
		Name:     "Unassigned Staff",
		Email:    "unassigned-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}

	_, err = Book(ctx, f.pool, f.orgID, BookInput{
		ServiceID:     f.serviceID,
		StaffUserID:   db.UUIDString(other.ID),
		CustomerName:  "Dana Customer",
		CustomerEmail: "dana@example.test",
		StartsAt:      nextMonday().Add(9 * time.Hour),
	})
	if !errors.Is(err, ErrStaffNotAssigned) {
		t.Fatalf("got %v, want ErrStaffNotAssigned", err)
	}
}

// TestIntegrationBookIsTenantScoped proves the booking path can't be used
// to reach across orgs: org B's service ID, supplied with org A's context,
// must be rejected rather than producing a cross-tenant appointment.
func TestIntegrationBookIsTenantScoped(t *testing.T) {
	orgA := newFixture(t)
	orgB := newFixture(t)
	ctx := context.Background()

	_, err := Book(ctx, orgA.pool, orgA.orgID, BookInput{
		ServiceID:     orgB.serviceID, // belongs to another tenant
		StaffUserID:   orgB.staffID,
		CustomerName:  "Cross Tenant",
		CustomerEmail: "cross@example.test",
		StartsAt:      nextMonday().Add(9 * time.Hour),
	})
	if err == nil {
		t.Fatal("booking org B's service from org A's context succeeded; expected rejection")
	}

	// Confirm nothing was written into either org for that slot.
	count, err := sqlc.New(orgA.pool).CountOverlappingAppointments(ctx, sqlc.CountOverlappingAppointmentsParams{
		OrgID:       db.MustUUID(orgB.orgID),
		StaffUserID: db.MustUUID(orgB.staffID),
		NewStartsAt: db.Timestamptz(nextMonday().Add(9 * time.Hour)),
		NewEndsAt:   db.Timestamptz(nextMonday().Add(10 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("count overlapping: %v", err)
	}
	if count != 0 {
		t.Fatalf("cross-tenant booking created %d appointment(s) in org B", count)
	}
}
