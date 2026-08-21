//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// Suspending an account is only worth anything if it takes effect on the
// session the person is already holding, not merely on their next login.
// That is a claim about two tables agreeing, so it is proved here rather
// than against a fake.
func TestIntegrationSuspendingStaffEndsTheirSessionAndBlocksLogin(t *testing.T) {
	ctx, pool, sessions := disableTestEnv(t)

	admin := signupOrg(t, ctx, pool, sessions)

	staff, err := CreateStaffUser(ctx, pool, admin.OrgID, CreateStaffInput{
		Name:     "Suspendable Staff",
		Email:    "staff-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}

	staffIdent, err := Login(ctx, pool, sessions, staff.Email, "password123", "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("staff login before suspension: %v", err)
	}
	if _, err := sessions.Get(ctx, staffIdent.Token); err != nil {
		t.Fatalf("staff session before suspension: %v", err)
	}

	if _, err := SetStaffDisabled(ctx, pool, admin.OrgID, db.UUIDString(staff.ID), true); err != nil {
		t.Fatalf("suspend staff: %v", err)
	}

	// The token they are still carrying must stop authenticating them.
	if _, err := sessions.Get(ctx, staffIdent.Token); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("suspended staff session resolved with %v, want session.ErrNotFound", err)
	}
	// And the row is gone, not merely ignored: a sessions table full of
	// tokens that authenticate nobody cannot be read for who is signed in.
	if _, err := sqlc.New(pool).GetSessionWithUser(ctx, staffIdent.Token); err == nil {
		t.Error("suspended staff's session row is still present")
	}
	// Signing in again must not hand them a fresh one.
	if _, err := Login(ctx, pool, sessions, staff.Email, "password123", "test", "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("suspended staff login returned %v, want ErrInvalidCredentials", err)
	}

	// Restoring the account lets them back in — with a new session, since
	// the old one was deleted rather than parked.
	if _, err := SetStaffDisabled(ctx, pool, admin.OrgID, db.UUIDString(staff.ID), false); err != nil {
		t.Fatalf("restore staff: %v", err)
	}
	restored, err := Login(ctx, pool, sessions, staff.Email, "password123", "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("restored staff login: %v", err)
	}
	if restored.Token == staffIdent.Token {
		t.Error("restored staff got their pre-suspension token back")
	}
}

// The suspension route is the one place in the app where one user acts on
// another's ability to sign in, so the two ways it could be abused are
// worth proving closed: reaching into another tenant, and reaching an
// admin account — including the caller's own, which would lock an
// organisation out of itself.
func TestIntegrationSuspendRefusesOtherTenantsAndAdmins(t *testing.T) {
	ctx, pool, sessions := disableTestEnv(t)

	orgA := signupOrg(t, ctx, pool, sessions)
	orgB := signupOrg(t, ctx, pool, sessions)

	staffB, err := CreateStaffUser(ctx, pool, orgB.OrgID, CreateStaffInput{
		Name:     "Org B Staff",
		Email:    "staff-b-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create org B staff: %v", err)
	}

	// Org A naming org B's real staff ID must read as "no such account".
	if _, err := SetStaffDisabled(ctx, pool, orgA.OrgID, db.UUIDString(staffB.ID), true); !errors.Is(err, ErrStaffNotFound) {
		t.Errorf("cross-tenant suspend returned %v, want ErrStaffNotFound", err)
	}
	if _, err := Login(ctx, pool, sessions, staffB.Email, "password123", "test", "127.0.0.1"); err != nil {
		t.Errorf("org B's staff lost access to a suspension org A asked for: %v", err)
	}

	// An admin suspending themselves is refused by the query's role
	// filter, not by a check the handler could forget.
	if _, err := SetStaffDisabled(ctx, pool, orgA.OrgID, orgA.UserID, true); !errors.Is(err, ErrStaffNotFound) {
		t.Errorf("admin self-suspend returned %v, want ErrStaffNotFound", err)
	}
	if _, err := sessions.Get(ctx, orgA.Token); err != nil {
		t.Errorf("admin lost their own session to a refused self-suspension: %v", err)
	}

	// A malformed ID is ordinary bad input from a form, not a panic.
	if _, err := SetStaffDisabled(ctx, pool, orgA.OrgID, "not-a-uuid", true); !errors.Is(err, ErrStaffNotFound) {
		t.Errorf("malformed staff ID returned %v, want ErrStaffNotFound", err)
	}
}

func disableTestEnv(t *testing.T) (context.Context, *pgxpool.Pool, *session.Store) {
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

	return ctx, pool, session.NewStore(pool, clock.Real{})
}

func signupOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessions *session.Store) session.Identity {
	t.Helper()

	ident, err := Signup(ctx, pool, sessions, SignupInput{
		OrgName:    "Suspension Test Org " + id.New(),
		AdminName:  "Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	return ident
}
