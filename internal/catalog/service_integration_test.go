//go:build integration

package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/db"
)

// A pasted name with leading/trailing whitespace is a validation non-issue
// (validate() already trims before checking it's non-empty), but storing
// it untrimmed would leave the whitespace in the services table and the
// edit form forever. Every other input in this app that reaches a name
// field — queue.Create, org.UpdateSettings, auth.Signup, ticket.Issue — is
// trimmed before the write; this proves CreateService and UpdateService
// hold to the same rule.
func TestIntegrationServiceNameIsTrimmedOnCreateAndUpdate(t *testing.T) {
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

	orgID, _ := seedOrgWithStaff(t, ctx, pool)

	svc, err := CreateService(ctx, pool, orgID, ServiceInput{
		Name:            "  Haircut  ",
		DurationMinutes: 30,
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	if svc.Name != "Haircut" {
		t.Errorf("created name = %q, want %q", svc.Name, "Haircut")
	}

	updated, err := UpdateService(ctx, pool, orgID, db.UUIDString(svc.ID), ServiceInput{
		Name:            "  Shave  ",
		DurationMinutes: 20,
		Active:          true,
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}
	if updated.Name != "Shave" {
		t.Errorf("updated name = %q, want %q", updated.Name, "Shave")
	}
}
