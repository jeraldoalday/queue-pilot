package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// ErrNoWaitingTickets means "call next" found nobody waiting. It is an
// ordinary outcome of an empty line, not a failure, and the console shows
// it as such.
var ErrNoWaitingTickets = errors.New("queue: no one is waiting")

// maxCustomerNameLen bounds what a kiosk visitor can type into the name
// field. The name is displayed on a shared board, so this is both a
// layout and an abuse bound.
const maxCustomerNameLen = 60

// maxPhoneLen bounds the optional callback number.
const maxPhoneLen = 32

type TicketInput struct {
	CustomerName  string
	CustomerPhone string
}

func (in TicketInput) validate() error {
	name := strings.TrimSpace(in.CustomerName)
	if name == "" {
		return ValidationError{"please enter a name"}
	}
	if len(name) > maxCustomerNameLen {
		return ValidationError{fmt.Sprintf("name must be %d characters or fewer", maxCustomerNameLen)}
	}
	if len(strings.TrimSpace(in.CustomerPhone)) > maxPhoneLen {
		return ValidationError{fmt.Sprintf("phone number must be %d characters or fewer", maxPhoneLen)}
	}
	return nil
}

// Issue creates a ticket in queueID for the service day `day`, which the
// caller must have resolved in the org's timezone (see org.Location) —
// passing a server-local day would put a late-evening walk-in on
// tomorrow's list for a business several hours behind UTC.
//
// It is the single ticket-creation path: the public kiosk and the staff
// console both call it, so the queue-closed rule and the numbering can't
// drift between them.
//
// The number allocation and the insert run in one transaction. If the
// insert fails, the counter increment rolls back with it, so an abandoned
// attempt doesn't leave a gap in the day's numbers — on a physical board,
// a skipped number reads as a lost customer.
func Issue(ctx context.Context, pool *pgxpool.Pool, orgID, queueID string, day time.Time, in TicketInput) (sqlc.QueueTicket, error) {
	if err := in.validate(); err != nil {
		return sqlc.QueueTicket{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return sqlc.QueueTicket{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	qtx := sqlc.New(tx)

	// Re-read the queue inside the transaction rather than trusting a
	// value the handler loaded earlier: an admin may have closed the line
	// in between, and the org scope here is what stops a guessed queue ID
	// from another tenant issuing a ticket.
	queue, err := qtx.GetQueue(ctx, sqlc.GetQueueParams{
		ID:    db.MustUUID(queueID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}
	if !queue.Active {
		return sqlc.QueueTicket{}, ErrQueueClosed
	}

	number, err := qtx.NextTicketNumber(ctx, sqlc.NextTicketNumberParams{
		QueueID:    queue.ID,
		TicketDate: db.Date(day),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	ticket, err := qtx.CreateTicket(ctx, sqlc.CreateTicketParams{
		ID:            db.MustUUID(id.New()),
		OrgID:         db.MustUUID(orgID),
		QueueID:       queue.ID,
		TicketDate:    db.Date(day),
		Number:        number,
		CustomerName:  strings.TrimSpace(in.CustomerName),
		CustomerPhone: db.Text(strings.TrimSpace(in.CustomerPhone)),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return sqlc.QueueTicket{}, err
	}
	return ticket, nil
}

// CallNext claims the lowest-numbered waiting ticket and marks it called
// by calledByUserID. It returns ErrNoWaitingTickets when the line is empty.
//
// The claim uses SELECT ... FOR UPDATE SKIP LOCKED inside a transaction,
// which is what makes several counters safe: if two staff press "call
// next" at the same instant, the second transaction skips the row the
// first has locked and takes the one after it. Without SKIP LOCKED the
// second would block, then wake up and hand the same customer to a second
// counter.
func CallNext(ctx context.Context, pool *pgxpool.Pool, orgID, queueID, calledByUserID string, day time.Time) (sqlc.QueueTicket, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return sqlc.QueueTicket{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	qtx := sqlc.New(tx)

	queue, err := qtx.GetQueue(ctx, sqlc.GetQueueParams{
		ID:    db.MustUUID(queueID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	next, err := qtx.LockNextWaitingTicket(ctx, sqlc.LockNextWaitingTicketParams{
		QueueID:    queue.ID,
		TicketDate: db.Date(day),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.QueueTicket{}, ErrNoWaitingTickets
		}
		return sqlc.QueueTicket{}, err
	}

	called, err := qtx.UpdateTicketStatus(ctx, sqlc.UpdateTicketStatusParams{
		ID:    next.ID,
		OrgID: db.MustUUID(orgID),
		// The row is locked by this transaction and was selected as
		// 'waiting', so the compare-and-swap can only match. It is still
		// passed rather than bypassed: the query has one contract, and a
		// caller exempting itself from it is how the guarantee erodes.
		FromStatus:     string(StatusWaiting),
		Status:         string(StatusCalled),
		CalledByUserID: db.UUIDOrNil(calledByUserID),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return sqlc.QueueTicket{}, err
	}
	return called, nil
}

// SetStatus moves one ticket through the lifecycle, rejecting transitions
// allowedTransitions doesn't permit. orgID must come from the caller's
// authenticated tenant context, so a ticket ID from another tenant reads
// as not found rather than being mutated.
//
// byUserID is recorded as the caller only when the ticket is being called;
// for every other transition it is left as-is, so finishing a service
// doesn't rewrite who originally called the number.
//
// The write is conditional on the ticket still being in the status this
// read saw. A busy counter is exactly where two staff act on one number
// within the same second — one marking it served, the other a no-show —
// and without that condition both moves are legal against the same stale
// read and the later write wins. Losing the race is reported as
// ErrInvalidTransition, which is what it is from the caller's side: by
// the time their request landed, the move was no longer legal.
func SetStatus(ctx context.Context, pool *pgxpool.Pool, orgID, ticketID string, to Status, byUserID string) (sqlc.QueueTicket, error) {
	q := sqlc.New(pool)

	current, err := q.GetTicket(ctx, sqlc.GetTicketParams{
		ID:    db.MustUUID(ticketID),
		OrgID: db.MustUUID(orgID),
	})
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	if !CanTransition(Status(current.Status), to) {
		return sqlc.QueueTicket{}, ErrInvalidTransition
	}

	var caller string
	if to == StatusCalled {
		caller = byUserID
	}

	updated, err := q.UpdateTicketStatus(ctx, sqlc.UpdateTicketStatusParams{
		ID:             db.MustUUID(ticketID),
		OrgID:          db.MustUUID(orgID),
		FromStatus:     current.Status,
		Status:         string(to),
		CalledByUserID: db.UUIDOrNil(caller),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The ticket exists — it was read a moment ago — so a zero-row
		// update means only one thing: someone else moved it first.
		return sqlc.QueueTicket{}, ErrInvalidTransition
	}
	return updated, err
}

// ListOpen returns the day's tickets that are still in play — waiting,
// called or being served. Both the staff console and the public board
// render exactly this set.
func ListOpen(ctx context.Context, pool *pgxpool.Pool, queueID string, day time.Time) ([]sqlc.QueueTicket, error) {
	return sqlc.New(pool).ListOpenTicketsForQueueOnDate(ctx, sqlc.ListOpenTicketsForQueueOnDateParams{
		QueueID:    db.MustUUID(queueID),
		TicketDate: db.Date(day),
	})
}

// ListForDay returns every ticket issued in the queue on that service day,
// finished ones included — the console's history view.
func ListForDay(ctx context.Context, pool *pgxpool.Pool, queueID string, day time.Time) ([]sqlc.QueueTicket, error) {
	return sqlc.New(pool).ListTicketsForQueueOnDate(ctx, sqlc.ListTicketsForQueueOnDateParams{
		QueueID:    db.MustUUID(queueID),
		TicketDate: db.Date(day),
	})
}

// GetTicketPublic resolves a ticket for the customer-facing status page.
// There is no session to scope by, so it scopes by the queue resolved from
// the URL instead: the ticket ID is an unguessable UUID, and a ticket
// belonging to a different queue reads as not found.
func GetTicketPublic(ctx context.Context, pool *pgxpool.Pool, queueID, ticketID string) (sqlc.QueueTicket, error) {
	tid, err := db.ParseUUID(ticketID)
	if err != nil {
		return sqlc.QueueTicket{}, pgx.ErrNoRows
	}
	return sqlc.New(pool).GetTicketInQueue(ctx, sqlc.GetTicketInQueueParams{
		ID:      tid,
		QueueID: db.MustUUID(queueID),
	})
}
