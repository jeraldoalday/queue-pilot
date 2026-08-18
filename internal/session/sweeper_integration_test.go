//go:build integration

package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// The sweep is one SQL statement, and the only thing worth proving about
// it is the thing a fake store cannot: that it removes the rows whose
// expiry has passed and leaves the live ones alone.
func TestIntegrationDeleteExpiredRemovesOnlyExpiredSessions(t *testing.T) {
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

	q := sqlc.New(pool)
	orgID, userID := seedUser(t, ctx, q)

	// One session already expired, one valid for another month.
	expiredToken := "test-expired-" + id.New()
	liveToken := "test-live-" + id.New()
	for token, expiry := range map[string]time.Time{
		expiredToken: time.Now().Add(-time.Hour),
		liveToken:    time.Now().Add(30 * 24 * time.Hour),
	} {
		if _, err := q.CreateSession(ctx, sqlc.CreateSessionParams{
			Token:     token,
			UserID:    db.MustUUID(userID),
			OrgID:     db.MustUUID(orgID),
			CsrfToken: id.New(),
			ExpiresAt: db.Timestamptz(expiry),
		}); err != nil {
			t.Fatalf("create session %s: %v", token, err)
		}
	}

	store := NewStore(pool, clock.Real{})
	removed, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if removed < 1 {
		t.Fatalf("DeleteExpired removed %d rows, want at least the one expired session", removed)
	}

	if _, err := q.GetSessionWithUser(ctx, expiredToken); err == nil {
		t.Error("the expired session row survived the sweep")
	}
	if _, err := q.GetSessionWithUser(ctx, liveToken); err != nil {
		t.Errorf("the live session was swept away too: %v", err)
	}

	// A second sweep must be safe to run against the same data.
	if _, err := store.DeleteExpired(ctx); err != nil {
		t.Errorf("a repeated sweep failed: %v", err)
	}
	if _, err := q.GetSessionWithUser(ctx, liveToken); err != nil {
		t.Errorf("the live session did not survive a second sweep: %v", err)
	}
}

// seedUser creates the org and user a session must point at, since
// sessions carry foreign keys to both.
func seedUser(t *testing.T, ctx context.Context, q *sqlc.Queries) (orgID, userID string) {
	t.Helper()

	org, err := q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		ID:       db.MustUUID(id.New()),
		Name:     "Sweeper Test Org",
		Slug:     "sweeper-" + id.New(),
		Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		ID:           db.MustUUID(id.New()),
		OrgID:        org.ID,
		Email:        "sweeper-" + id.New() + "@example.test",
		Name:         "Sweeper Test User",
		PasswordHash: "not-a-real-hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	return db.UUIDString(org.ID), db.UUIDString(user.ID)
}
