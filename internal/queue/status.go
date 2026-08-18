package queue

import "errors"

// Status mirrors the queue_tickets.status check constraint.
type Status string

const (
	// StatusWaiting is a ticket that has been issued but not yet called.
	StatusWaiting Status = "waiting"
	// StatusCalled means the number has been announced and the customer is
	// on their way to the counter.
	StatusCalled Status = "called"
	// StatusServing means the customer is at the counter being helped.
	StatusServing Status = "serving"
	// StatusDone is a completed service — the only outcome that counts
	// toward the average service time used for wait estimates.
	StatusDone Status = "done"
	// StatusNoShow means the number was called and nobody came.
	StatusNoShow Status = "no_show"
	// StatusCancelled means the customer left or the ticket was voided.
	StatusCancelled Status = "cancelled"
)

var ErrInvalidTransition = errors.New("queue: invalid ticket status transition")

// allowedTransitions is the single source of truth for the ticket
// lifecycle, kept as data for the same reason appointment.allowedTransitions
// is: a handler cannot invent a move the domain doesn't allow, and the
// whole state machine can be read in one place.
//
// The shape mirrors how a counter actually runs:
//
//	waiting  -> called            the number goes up on the board
//	called   -> serving           the customer arrived
//	called   -> waiting           nobody came yet, put them back in line
//	called   -> no_show           called, waited, gave up on them
//	serving  -> done              finished
//
// Cancellation is reachable from every open state, because a customer can
// walk out at any point before the service completes.
//
// Two absences are deliberate. serving -> no_show is impossible: someone
// being served has, by definition, shown up. And the three end states are
// terminal — a ticket's number has already been passed over on the board
// and its counter slot reused, so reopening it would put a customer back
// in a position the queue has moved past.
var allowedTransitions = map[Status][]Status{
	StatusWaiting:   {StatusCalled, StatusCancelled},
	StatusCalled:    {StatusServing, StatusWaiting, StatusNoShow, StatusCancelled},
	StatusServing:   {StatusDone, StatusCancelled},
	StatusDone:      {},
	StatusNoShow:    {},
	StatusCancelled: {},
}

// CanTransition reports whether the lifecycle permits moving from -> to.
func CanTransition(from, to Status) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// IsTerminal reports whether a ticket in this status can still change.
// The console uses it to decide whether to render any action buttons at
// all, so the UI and the domain agree on what "finished" means.
func IsTerminal(s Status) bool {
	return len(allowedTransitions[s]) == 0
}

// IsOpen reports whether a ticket is still in play — issued and not yet
// resolved. These are exactly the tickets the board and console show.
func IsOpen(s Status) bool {
	switch s {
	case StatusWaiting, StatusCalled, StatusServing:
		return true
	default:
		return false
	}
}

// ParseStatus converts a request-supplied status string into a Status,
// rejecting anything outside the known set so an arbitrary value can never
// reach the database.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusWaiting, StatusCalled, StatusServing, StatusDone, StatusNoShow, StatusCancelled:
		return Status(s), true
	default:
		return "", false
	}
}

// Label renders a status for display. The stored values are machine-shaped
// ("no_show"); this is the only place they're turned into words, so the
// board, the console and the customer's ticket page can't word the same
// state three different ways.
func Label(s Status) string {
	switch s {
	case StatusWaiting:
		return "Waiting"
	case StatusCalled:
		return "Called"
	case StatusServing:
		return "Being served"
	case StatusDone:
		return "Done"
	case StatusNoShow:
		return "No-show"
	case StatusCancelled:
		return "Cancelled"
	default:
		return string(s)
	}
}

// AllStatuses returns every valid ticket status. It is the enumeration the
// lifecycle tests iterate over, so a status added to the check constraint
// and to allowedTransitions but forgotten here fails a test rather than
// silently escaping coverage.
func AllStatuses() []Status {
	return []Status{
		StatusWaiting, StatusCalled, StatusServing,
		StatusDone, StatusNoShow, StatusCancelled,
	}
}
