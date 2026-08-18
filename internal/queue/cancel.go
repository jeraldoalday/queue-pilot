package queue

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// ErrNotCancellable means the ticket exists but is past the point where
// its holder can withdraw it — they are at the counter, or the ticket has
// already been resolved one way or another.
var ErrNotCancellable = errors.New("queue: this ticket can no longer be cancelled")

// CustomerCancellable reports whether the holder of a ticket in this
// status may withdraw it themselves.
//
// It is deliberately narrower than what the lifecycle allows staff to do.
// A waiting or called customer deciding not to stay is the whole point:
// their number leaves the line, everyone behind them moves up, and the
// counter stops calling a name nobody will answer to. Once they are being
// served they are standing at the counter, and a phone in their pocket
// cancelling the interaction they are in the middle of describes nothing
// real — the member of staff in front of them resolves it. Terminal
// statuses are terminal for everyone.
func CustomerCancellable(s Status) bool {
	return s == StatusWaiting || s == StatusCalled
}

// CancelByCustomer withdraws a ticket on its holder's own behalf, from the
// public ticket page.
//
// There is no session here, so the scope is the queue resolved from the
// URL plus the ticket's unguessable ID — the same pair that already gates
// reading the ticket. Nothing widens that: the org the write is charged to
// is read off the ticket row itself, never taken from the request, so a
// ticket ID from another queue resolves to not-found before any write is
// attempted.
//
// The write is the same compare-and-swap staff transitions use, so a
// customer tapping "leave the line" at the same moment a counter calls
// them does not produce two winners: one of the two is told the ticket
// moved on, and the queue keeps a single coherent history.
func CancelByCustomer(ctx context.Context, pool *pgxpool.Pool, queueID, ticketID string) (sqlc.QueueTicket, error) {
	ticket, err := GetTicketPublic(ctx, pool, queueID, ticketID)
	if err != nil {
		return sqlc.QueueTicket{}, err
	}

	if !CustomerCancellable(Status(ticket.Status)) {
		return sqlc.QueueTicket{}, ErrNotCancellable
	}

	cancelled, err := sqlc.New(pool).UpdateTicketStatus(ctx, sqlc.UpdateTicketStatusParams{
		ID:         ticket.ID,
		OrgID:      ticket.OrgID,
		FromStatus: ticket.Status,
		Status:     string(StatusCancelled),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row was read a moment ago, so no rows updated means a
		// counter moved the ticket between the read and the write.
		return sqlc.QueueTicket{}, ErrNotCancellable
	}
	return cancelled, err
}

// ticketCancelURL is where the "leave the line" form posts.
func ticketCancelURL(orgSlug, queueSlug, ticketID string) string {
	return ticketURL(orgSlug, queueSlug, ticketID) + "/cancel"
}
