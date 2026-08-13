//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// TestIntegrationCrossTenantIsolation is the single most important test in
// this codebase: it proves that a query scoped to org A's ID cannot read
// org B's data, even when it targets a real, existing row by ID. Every
// domain query in the app follows the same shape (WHERE id = $1 AND
// org_id = $2) specifically so this guarantee holds everywhere, not just
// for users — this test exercises the pattern at its origin point, the
// users table populated by signup.
func TestIntegrationCrossTenantIsolation(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; run via `make test-integration`")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	sessions := session.NewStore(pool, clock.Real{})

	identA, err := Signup(ctx, pool, sessions, SignupInput{
		OrgName:    "Isolation Test Org A " + id.New(),
		AdminName:  "Admin A",
		AdminEmail: "admin-a-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup org A: %v", err)
	}

	identB, err := Signup(ctx, pool, sessions, SignupInput{
		OrgName:    "Isolation Test Org B " + id.New(),
		AdminName:  "Admin B",
		AdminEmail: "admin-b-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup org B: %v", err)
	}

	q := sqlc.New(pool)

	// Org A must NOT be able to fetch org B's user by ID, even though the
	// ID is valid and the row exists.
	_, err = q.GetUserByID(ctx, sqlc.GetUserByIDParams{
		ID:    db.MustUUID(identB.UserID),
		OrgID: db.MustUUID(identA.OrgID),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows fetching org B's user scoped to org A, got: %v", err)
	}

	// Sanity check: org B can fetch its own user with its own org ID —
	// proves the failure above is isolation working, not a broken query.
	_, err = q.GetUserByID(ctx, sqlc.GetUserByIDParams{
		ID:    db.MustUUID(identB.UserID),
		OrgID: db.MustUUID(identB.OrgID),
	})
	if err != nil {
		t.Fatalf("org B fetching its own user failed: %v", err)
	}

	// A session created for org A's user must resolve to org A, never org
	// B, when looked up by the session layer that RequireAuth relies on.
	got, err := sessions.Get(ctx, identA.Token)
	if err != nil {
		t.Fatalf("resolve org A session: %v", err)
	}
	if got.OrgID != identA.OrgID {
		t.Fatalf("session for org A resolved to org %q, want %q", got.OrgID, identA.OrgID)
	}
}
