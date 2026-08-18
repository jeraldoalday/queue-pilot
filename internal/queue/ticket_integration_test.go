//go:build integration

package queue

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// fixture is an org with one staff member and one open walk-in queue.
type fixture struct {
	pool    *pgxpool.Pool
	orgID   string
	staffID string
	queueID string
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
		OrgName:    "Queue Test Org " + id.New(),
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

	q, err := Create(ctx, pool, ident.OrgID, Input{Name: "Walk-ins"})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}

	return fixture{
		pool:    pool,
		orgID:   ident.OrgID,
		staffID: db.UUIDString(staff.ID),
		queueID: db.UUIDString(q.ID),
	}
}

// testDay returns a date far enough in the future that a run never shares
// a service day with rows another run left behind. Each fixture builds a
// fresh org anyway, but a distinct day keeps the numbers readable when a
// failing test is inspected by hand.
func testDay() time.Time {
	return time.Date(2100, time.March, 1, 0, 0, 0, 0, time.UTC)
}

func mustIssue(t *testing.T, f fixture, name string) sqlc.QueueTicket {
	t.Helper()
	ticket, err := Issue(context.Background(), f.pool, f.orgID, f.queueID, testDay(), TicketInput{CustomerName: name})
	if err != nil {
		t.Fatalf("issue ticket for %s: %v", name, err)
	}
	return ticket
}

func TestIntegrationTicketNumbersStartAtOneAndIncrement(t *testing.T) {
	f := newFixture(t)

	for want := int32(1); want <= 3; want++ {
		ticket := mustIssue(t, f, "Customer")
		if ticket.Number != want {
			t.Fatalf("ticket number = %d, want %d", ticket.Number, want)
		}
		if ticket.Status != string(StatusWaiting) {
			t.Fatalf("new ticket status = %q, want %q", ticket.Status, StatusWaiting)
		}
	}
}

// Numbering is per service day: a queue that ran yesterday starts again at
// #1 today rather than continuing from wherever it left off.
func TestIntegrationTicketNumbersResetEachDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	mustIssue(t, f, "Yesterday")

	tomorrow := testDay().AddDate(0, 0, 1)
	ticket, err := Issue(ctx, f.pool, f.orgID, f.queueID, tomorrow, TicketInput{CustomerName: "Tomorrow"})
	if err != nil {
		t.Fatalf("issue on the next day: %v", err)
	}
	if ticket.Number != 1 {
		t.Fatalf("first ticket of a new day = #%d, want #1", ticket.Number)
	}
}

// The guarantee that matters at a kiosk: several people tapping "get a
// ticket" at the same moment must each get their own number. This would
// pass by luck with a read-then-write counter, so it runs 12 concurrent
// issues and asserts the numbers form exactly 1..12 with no gaps and no
// duplicates.
func TestIntegrationConcurrentIssuesGetDistinctNumbers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const n = 12
	var wg sync.WaitGroup
	numbers := make([]int32, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ticket, err := Issue(ctx, f.pool, f.orgID, f.queueID, testDay(), TicketInput{CustomerName: "Racer"})
			numbers[i], errs[i] = ticket.Number, err
		}(i)
	}
	wg.Wait()

	seen := make(map[int32]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent issue %d failed: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("ticket number %d was issued twice", numbers[i])
		}
		seen[numbers[i]] = true
	}
	for want := int32(1); want <= n; want++ {
		if !seen[want] {
			t.Fatalf("ticket number %d was never issued — the day's numbers have a gap", want)
		}
	}
}

// Two counters pressing "call next" at the same instant must be handed two
// different customers. Without FOR UPDATE SKIP LOCKED the second
// transaction would block on the first's row, then wake and call the same
// person to a second counter.
func TestIntegrationConcurrentCallNextNeverDuplicates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const n = 6
	for i := 0; i < n; i++ {
		mustIssue(t, f, "Customer")
	}

	var wg sync.WaitGroup
	called := make([]sqlc.QueueTicket, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			called[i], errs[i] = CallNext(ctx, f.pool, f.orgID, f.queueID, f.staffID, testDay())
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent call-next %d failed: %v", i, err)
		}
		ticketID := db.UUIDString(called[i].ID)
		if seen[ticketID] {
			t.Fatalf("ticket %s was called by two counters at once", ticketID)
		}
		seen[ticketID] = true
	}
}

func TestIntegrationCallNextTakesLowestNumberFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	first := mustIssue(t, f, "First")
	mustIssue(t, f, "Second")

	called, err := CallNext(ctx, f.pool, f.orgID, f.queueID, f.staffID, testDay())
	if err != nil {
		t.Fatalf("call next: %v", err)
	}
	if db.UUIDString(called.ID) != db.UUIDString(first.ID) {
		t.Fatalf("call next took #%d, want the lowest waiting number #%d", called.Number, first.Number)
	}
	if called.Status != string(StatusCalled) {
		t.Fatalf("called ticket status = %q, want %q", called.Status, StatusCalled)
	}
	if !called.CalledAt.Valid {
		t.Fatal("called_at was not stamped by the transition")
	}
	if db.UUIDString(called.CalledByUserID) != f.staffID {
		t.Fatal("called_by_user_id does not record the staff member who called")
	}
}

func TestIntegrationCallNextOnEmptyQueue(t *testing.T) {
	f := newFixture(t)

	_, err := CallNext(context.Background(), f.pool, f.orgID, f.queueID, f.staffID, testDay())
	if !errors.Is(err, ErrNoWaitingTickets) {
		t.Fatalf("call next on an empty queue = %v, want ErrNoWaitingTickets", err)
	}
}

// The full happy path, with the lifecycle timestamps checked at each step:
// they are stamped by the same statement that moves the ticket, so a
// status can never be recorded without its time.
func TestIntegrationTicketLifecycleStampsTimestamps(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	ticket := mustIssue(t, f, "Ada")
	ticketID := db.UUIDString(ticket.ID)

	called, err := SetStatus(ctx, f.pool, f.orgID, ticketID, StatusCalled, f.staffID)
	if err != nil {
		t.Fatalf("to called: %v", err)
	}
	if !called.CalledAt.Valid || called.ServingAt.Valid || called.CompletedAt.Valid {
		t.Fatal("calling a ticket should stamp called_at and nothing else")
	}

	serving, err := SetStatus(ctx, f.pool, f.orgID, ticketID, StatusServing, f.staffID)
	if err != nil {
		t.Fatalf("to serving: %v", err)
	}
	if !serving.ServingAt.Valid || serving.CompletedAt.Valid {
		t.Fatal("starting service should stamp serving_at and not completed_at")
	}

	done, err := SetStatus(ctx, f.pool, f.orgID, ticketID, StatusDone, f.staffID)
	if err != nil {
		t.Fatalf("to done: %v", err)
	}
	if !done.CompletedAt.Valid {
		t.Fatal("completing a ticket should stamp completed_at")
	}
	// The whole point of keeping who-called separate from later moves.
	if db.UUIDString(done.CalledByUserID) != f.staffID {
		t.Fatal("completing the ticket erased who called it")
	}
}

func TestIntegrationTerminalTicketCannotReopen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	ticket := mustIssue(t, f, "Ada")
	ticketID := db.UUIDString(ticket.ID)

	if _, err := SetStatus(ctx, f.pool, f.orgID, ticketID, StatusCancelled, f.staffID); err != nil {
		t.Fatalf("to cancelled: %v", err)
	}
	if _, err := SetStatus(ctx, f.pool, f.orgID, ticketID, StatusWaiting, f.staffID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reopening a cancelled ticket = %v, want ErrInvalidTransition", err)
	}
}

func TestIntegrationClosedQueueRefusesTickets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := Update(ctx, f.pool, f.orgID, f.queueID, Input{Name: "Walk-ins", Active: false}); err != nil {
		t.Fatalf("close queue: %v", err)
	}

	_, err := Issue(ctx, f.pool, f.orgID, f.queueID, testDay(), TicketInput{CustomerName: "Ada"})
	if !errors.Is(err, ErrQueueClosed) {
		t.Fatalf("issuing into a closed queue = %v, want ErrQueueClosed", err)
	}
}

// Tenant isolation, the same shape proven for appointments: org B holding
// a valid ticket ID from org A must not be able to move it.
func TestIntegrationCannotTouchAnotherOrgsTicket(t *testing.T) {
	orgA := newFixture(t)
	orgB := newFixture(t)
	ctx := context.Background()

	ticket := mustIssue(t, orgA, "Ada")

	_, err := SetStatus(ctx, orgB.pool, orgB.orgID, db.UUIDString(ticket.ID), StatusCancelled, orgB.staffID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-tenant status change = %v, want pgx.ErrNoRows", err)
	}

	// And it is genuinely untouched, not merely reported as missing.
	after, err := sqlc.New(orgA.pool).GetTicket(ctx, sqlc.GetTicketParams{
		ID:    ticket.ID,
		OrgID: db.MustUUID(orgA.orgID),
	})
	if err != nil {
		t.Fatalf("re-read ticket: %v", err)
	}
	if after.Status != string(StatusWaiting) {
		t.Fatalf("ticket status = %q after a cross-tenant attempt, want %q", after.Status, StatusWaiting)
	}
}

// A queue slug from another org must not resolve, even when both orgs
// named their line the same obvious thing.
func TestIntegrationQueueSlugIsScopedToOrg(t *testing.T) {
	orgA := newFixture(t)
	orgB := newFixture(t)
	ctx := context.Background()

	qa, err := Get(ctx, orgA.pool, orgA.orgID, orgA.queueID)
	if err != nil {
		t.Fatalf("get org A queue: %v", err)
	}

	if _, err := GetBySlug(ctx, orgB.pool, orgB.orgID, qa.Slug); err != nil {
		t.Fatalf("org B should have its own queue with the same slug: %v", err)
	}

	found, err := GetBySlug(ctx, orgB.pool, orgB.orgID, qa.Slug)
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if db.UUIDString(found.ID) == db.UUIDString(qa.ID) {
		t.Fatal("org B's slug lookup resolved to org A's queue")
	}
}
