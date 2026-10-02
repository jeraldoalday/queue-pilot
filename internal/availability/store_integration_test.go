//go:build integration

package availability

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// newFixture sets up one org with one staff member and no availability yet,
// the same shape appointment's integration fixture uses.
func newFixture(t *testing.T) (pool *pgxpool.Pool, orgID, staffID string) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; run via `make test-integration`")
	}

	ctx := context.Background()
	p, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)

	sessions := session.NewStore(p, clock.Real{})
	ident, err := auth.Signup(ctx, p, sessions, auth.SignupInput{
		OrgName:    "Availability Test Org " + id.New(),
		AdminName:  "Test Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	staff, err := auth.CreateStaffUser(ctx, p, ident.OrgID, auth.CreateStaffInput{
		Name:     "Test Staff",
		Email:    "staff-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}

	return p, ident.OrgID, db.UUIDString(staff.ID)
}

// TestIntegrationConcurrentAddHoursOnlyOneWins is the availability
// counterpart of appointment's TestIntegrationConcurrentBookingsOnlyOneWins:
// several goroutines race to add the identical overlapping window for one
// staff member, and the staff_availability_no_overlap constraint must let
// exactly one through regardless of how the app-level check in AddHours
// happened to interleave with the others.
func TestIntegrationConcurrentAddHoursOnlyOneWins(t *testing.T) {
	pool, orgID, staffID := newFixture(t)
	ctx := context.Background()

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
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			_, err := AddHours(ctx, pool, orgID, staffID, HoursInput{
				Weekday:  int(time.Monday),
				StartMin: 9 * 60,
				EndMin:   17 * 60,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okCount++
			} else {
				errs = append(errs, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if okCount != 1 {
		t.Fatalf("got %d successful AddHours calls for the same window, want exactly 1 (errors: %v)", okCount, errs)
	}
	for _, err := range errs {
		var verr ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("losing AddHours call failed with %v (%T), want a ValidationError", err, err)
		}
	}

	hours, err := ListHours(ctx, pool, orgID, staffID)
	if err != nil {
		t.Fatalf("list hours: %v", err)
	}
	if len(hours) != 1 {
		t.Fatalf("got %d stored windows after the race, want exactly 1", len(hours))
	}
}

// TestIntegrationAddHoursRejectsOverlapAcrossRequests proves the same
// constraint catches a plain sequential overlap too — not only the
// concurrent race — once the window has actually committed, independent of
// whether the in-process ListHours cache that AddHours's own check reads
// happens to be stale.
func TestIntegrationAddHoursRejectsOverlapAcrossRequests(t *testing.T) {
	pool, orgID, staffID := newFixture(t)
	ctx := context.Background()

	if _, err := AddHours(ctx, pool, orgID, staffID, HoursInput{
		Weekday:  int(time.Tuesday),
		StartMin: 9 * 60,
		EndMin:   17 * 60,
	}); err != nil {
		t.Fatalf("add first window: %v", err)
	}

	_, err := AddHours(ctx, pool, orgID, staffID, HoursInput{
		Weekday:  int(time.Tuesday),
		StartMin: 12 * 60,
		EndMin:   13 * 60,
	})
	var verr ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("got %v (%T), want a ValidationError", err, err)
	}

	hours, err := ListHours(ctx, pool, orgID, staffID)
	if err != nil {
		t.Fatalf("list hours: %v", err)
	}
	if len(hours) != 1 {
		t.Fatalf("got %d stored windows, want exactly 1 (the overlapping one must not have been inserted)", len(hours))
	}
}
