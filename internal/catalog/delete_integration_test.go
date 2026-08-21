//go:build integration

package catalog

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// Whether a service can be deleted is decided by a foreign key, so it can
// only be proved against a real database. What is being checked is not the
// constraint itself but that the app reports it as the one thing an admin
// can act on, rather than as an unexplained failure.
func TestIntegrationDeletingAServiceWithHistoryIsRefusedClearly(t *testing.T) {
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

	orgID, staffID := seedOrgWithStaff(t, ctx, pool)

	svc, err := CreateService(ctx, pool, orgID, ServiceInput{
		Name:            "Consultation",
		DurationMinutes: 30,
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	serviceID := db.UUIDString(svc.ID)

	// Unused, it deletes cleanly — the check below is about history, not
	// about deletion being broken.
	spare, err := CreateService(ctx, pool, orgID, ServiceInput{Name: "Spare", DurationMinutes: 15, Active: true})
	if err != nil {
		t.Fatalf("create spare service: %v", err)
	}
	if err := DeleteService(ctx, pool, orgID, db.UUIDString(spare.ID)); err != nil {
		t.Fatalf("deleting an unused service failed: %v", err)
	}

	// One appointment against the first service is all it takes.
	start := time.Now().Add(48 * time.Hour)
	if _, err := sqlc.New(pool).CreateAppointment(ctx, sqlc.CreateAppointmentParams{
		ID:            db.MustUUID(id.New()),
		OrgID:         db.MustUUID(orgID),
		ServiceID:     svc.ID,
		StaffUserID:   db.MustUUID(staffID),
		CustomerName:  "Ada Lovelace",
		CustomerEmail: "ada@example.test",
		StartsAt:      db.Timestamptz(start),
		EndsAt:        db.Timestamptz(start.Add(30 * time.Minute)),
	}); err != nil {
		t.Fatalf("create appointment: %v", err)
	}

	if err := DeleteService(ctx, pool, orgID, serviceID); !errors.Is(err, ErrServiceInUse) {
		t.Fatalf("deleting a service with an appointment returned %v, want ErrServiceInUse", err)
	}

	// The service is still there — a refused delete must not half-happen.
	if _, err := GetService(ctx, pool, orgID, serviceID); err != nil {
		t.Fatalf("the service disappeared despite the delete being refused: %v", err)
	}

	// And the way forward the error message names actually works.
	updated, err := UpdateService(ctx, pool, orgID, serviceID, ServiceInput{
		Name:            "Consultation",
		DurationMinutes: 30,
		Active:          false,
	})
	if err != nil {
		t.Fatalf("deactivating the service failed: %v", err)
	}
	if updated.Active {
		t.Error("the service is still active after being turned off")
	}
}

func seedOrgWithStaff(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (orgID, staffID string) {
	t.Helper()

	ident, err := auth.Signup(ctx, pool, session.NewStore(pool, clock.Real{}), auth.SignupInput{
		OrgName:    "Service Delete Org " + id.New(),
		AdminName:  "Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	staff, err := auth.CreateStaffUser(ctx, pool, ident.OrgID, auth.CreateStaffInput{
		Name:     "Rosa Diaz",
		Email:    "staff-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	return ident.OrgID, db.UUIDString(staff.ID)
}
