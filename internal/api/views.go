package api

import (
	"time"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/queue"
)

// The types in this file are the API's vocabulary, and they are built by
// pure functions from already-loaded rows. That split is what lets the
// shape of every payload be tested without a database, and it is what
// stops a field being added to a row and forwarded to the public without
// anyone deciding to.
//
// Times go out as RFC 3339 with the org's offset, never as a wall-clock
// string. A front end rendering "14:00" has to know which 14:00, and the
// answer is always the business's own — a customer in another timezone
// reading a clinic's page wants the clinic's hours, not their own.

// Org is the shop front: who this is and what they offer.
type Org struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Timezone string `json:"timezone"`
	// LocalTime is the org's own clock at the moment of the response. The
	// front end needs it to decide what "today" means without guessing
	// from the viewer's device, which is routinely wrong by a day for
	// anyone reading from another continent.
	LocalTime time.Time `json:"local_time"`
}

// NewOrg renders an organization row for the public API. It deliberately
// omits the ID: nothing a caller can do needs it, and the slug is the
// public name for the same thing.
func NewOrg(o sqlc.Organization, now time.Time) Org {
	return Org{
		Name:      o.Name,
		Slug:      o.Slug,
		Timezone:  o.Timezone,
		LocalTime: now,
	}
}

// Service is one bookable service on the public list.
type Service struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	DurationMinutes int32  `json:"duration_minutes"`
	// PriceCents is null when the org has not set one, rather than zero.
	// A service priced at zero is free, which is a different claim from a
	// service whose price is not published here.
	PriceCents *int32 `json:"price_cents"`
}

// NewService renders a service row. The service ID stays internal for the
// same reason the org ID does: the slug already identifies it in a URL,
// and it is the one of the two that survives being printed on a poster.
func NewService(s sqlc.Service) Service {
	out := Service{
		Slug:            s.Slug,
		Name:            s.Name,
		Description:     s.Description,
		DurationMinutes: s.DurationMinutes,
	}
	if s.PriceCents.Valid {
		cents := s.PriceCents.Int32
		out.PriceCents = &cents
	}
	return out
}

// NewServices renders a list, always as an array. A nil slice would encode
// as null, and a front end mapping over the result would then have to
// guard a case the API has no reason to produce: an org with no bookable
// services has an empty list, not a missing one.
func NewServices(rows []sqlc.Service) []Service {
	out := make([]Service, 0, len(rows))
	for _, s := range rows {
		out = append(out, NewService(s))
	}
	return out
}

// QueueSummary is one line as it appears on a list of them: enough to
// choose between two queues, not enough to be a board.
type QueueSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Open bool   `json:"open"`
	// Waiting and EstimatedWaitMinutes are what somebody standing in a
	// doorway with two lines in front of them is actually choosing on.
	Waiting              int `json:"waiting"`
	EstimatedWaitMinutes int `json:"estimated_wait_minutes"`
}

// NewQueueSummary folds a loaded board down to the list view of it.
func NewQueueSummary(board queue.Board) QueueSummary {
	return QueueSummary{
		Slug:                 board.Queue.Slug,
		Name:                 board.Queue.Name,
		Open:                 board.Queue.Active,
		Waiting:              board.Stats.Waiting,
		EstimatedWaitMinutes: waitMinutes(queue.Estimate(board.Stats.Waiting, board.ServiceTime, board.ActiveServers())),
	}
}

// Ticket is a customer's own number, returned only to whoever holds its
// unguessable ID.
//
// This is the one payload that carries a name, and it carries it because
// the holder typed it and is the only one who can ask for it. The board
// and the summary above never do.
type Ticket struct {
	ID           string `json:"id"`
	Number       int32  `json:"number"`
	CustomerName string `json:"customer_name"`
	Status       string `json:"status"`
	StatusLabel  string `json:"status_label"`
	// Position counts the holder themselves, so the front of the line is
	// 1 and never 0. It is null once the ticket is no longer waiting:
	// somebody at the counter has no place in a line.
	Position *int `json:"position"`
	// EstimatedWaitMinutes is null for the same reason, and for resolved
	// tickets. A cancelled ticket with a wait attached invites a front end
	// to render a countdown for a customer who has left.
	EstimatedWaitMinutes *int `json:"estimated_wait_minutes"`
	// Cancellable says whether the "leave the line" action applies. The
	// front end could derive it from the status, but then the rule would
	// exist in two languages and only one of them has the tests.
	Cancellable bool      `json:"cancellable"`
	ServiceDay  string    `json:"service_day"`
	IssuedAt    time.Time `json:"issued_at"`
}

// NewTicket renders a ticket against the board of its own service day —
// which is the board the caller must load, not today's. A customer
// reopening yesterday's link should see that ticket's day rather than a
// position in a line it was never part of.
func NewTicket(t sqlc.QueueTicket, board queue.Board, loc *time.Location) Ticket {
	status := queue.Status(t.Status)
	out := Ticket{
		ID:           db.UUIDString(t.ID),
		Number:       t.Number,
		CustomerName: t.CustomerName,
		Status:       string(status),
		StatusLabel:  queue.Label(status),
		Cancellable:  queue.CustomerCancellable(status),
		ServiceDay:   board.Day.Format(serviceDayLayout),
		IssuedAt:     db.Time(t.CreatedAt).In(locOrUTC(loc)),
	}
	if status == queue.StatusWaiting {
		position := queue.Position(board.AheadOf(t.Number))
		minutes := waitMinutes(board.EstimateFor(t.Number))
		out.Position = &position
		out.EstimatedWaitMinutes = &minutes
	}
	return out
}

// Slot is one bookable time on the public booking page.
//
// Token is the value that has to come back to book it. It names the
// instant and the staff member the page assigned, because a radio button
// carries one value and the two facts must travel together. It is not
// trusted on the way back: the booking endpoint re-derives the day's slots
// and matches, so a forged token names a slot that is not on offer and is
// refused like any other.
//
// StaffName is included so a confirmation can say who the customer is
// seeing. The customer is never asked to choose — on the public side the
// question is "when can I come in", and answering it with a directory of
// employees is a worse page.
type Slot struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	StaffName string    `json:"staff_name"`
	Token     string    `json:"token"`
}

// NewSlots renders the day's open slots. Like NewServices it always
// produces an array: a fully-booked Tuesday is an empty list.
func NewSlots(open []appointment.OpenSlot, loc *time.Location) []Slot {
	loc = locOrUTC(loc)
	out := make([]Slot, 0, len(open))
	for _, s := range open {
		out = append(out, Slot{
			Start:     s.Start.In(loc),
			End:       s.End.In(loc),
			StaffName: s.StaffName,
			Token:     appointment.SlotToken(s, loc),
		})
	}
	return out
}

// Appointment is a customer's own booking, returned only to whoever holds
// its unguessable ID — the same rule as Ticket, and it carries a name for
// the same reason.
type Appointment struct {
	ID          string    `json:"id"`
	Service     string    `json:"service"`
	StaffName   string    `json:"staff_name"`
	Customer    string    `json:"customer_name"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Status      string    `json:"status"`
	Cancellable bool      `json:"cancellable"`
}

// NewAppointment renders a booking for its holder.
//
// Cancellable answers the only question the page has an action for, and it
// answers it the way CancelPublic will: still booked, and not yet started.
// Deriving it here rather than in the front end keeps one rule in one
// place — a button offered for an appointment that began ten minutes ago
// is a button that fails when pressed.
func NewAppointment(a sqlc.GetAppointmentWithNamesRow, now time.Time, loc *time.Location) Appointment {
	loc = locOrUTC(loc)
	starts := db.Time(a.StartsAt).In(loc)
	return Appointment{
		ID:          db.UUIDString(a.ID),
		Service:     a.ServiceName,
		StaffName:   a.StaffName,
		Customer:    a.CustomerName,
		StartsAt:    starts,
		EndsAt:      db.Time(a.EndsAt).In(loc),
		Status:      a.Status,
		Cancellable: appointment.Status(a.Status) == appointment.StatusBooked && starts.After(now),
	}
}

// serviceDayLayout is the date form every payload uses. It matches the one
// the existing board API already serves, so a caller reading both is not
// parsing two formats.
const serviceDayLayout = "2006-01-02"

// waitMinutes converts an estimate to the whole minutes the wire carries.
// The duration is already rounded up to five minutes by queue.Estimate, so
// truncating here loses nothing.
func waitMinutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d.Minutes())
}

// locOrUTC is the same fallback org.LocationOf makes: a wrong-but-
// consistent zone degrades better than a nil dereference.
func locOrUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}
