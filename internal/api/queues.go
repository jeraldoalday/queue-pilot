package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/queue"
)

// orgContext is a resolved {orgSlug}: which business, what timezone, and
// what "today" means to them. It is the JSON side of the same
// publicContext the HTML pages resolve, and it exists for the same reason
// — so every lookup underneath is scoped by an org ID that came from the
// path and from nowhere else.
type orgContext struct {
	row sqlc.Organization
	loc *time.Location
	now time.Time
	day time.Time
}

func (o orgContext) id() string { return db.UUIDString(o.row.ID) }

// resolveOrg turns the {orgSlug} path segment into an orgContext.
func (h *Handlers) resolveOrg(ctx context.Context, orgSlug string) (orgContext, error) {
	row, err := org.BySlug(ctx, h.pool, orgSlug)
	if err != nil {
		return orgContext{}, err
	}
	loc := org.LocationOf(row)
	now := h.now().In(loc)
	return orgContext{
		row: row,
		loc: loc,
		now: now,
		day: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc),
	}, nil
}

// OrgSummary is what a front end loads first: who this business is, what
// they can be booked for, and which lines are open right now.
//
// It is one response rather than three because it is one screen. A landing
// page that fires three requests renders in three stages, and the stage a
// customer sees longest is the empty one.
type OrgSummary struct {
	Org      Org            `json:"org"`
	Services []Service      `json:"services"`
	Queues   []QueueSummary `json:"queues"`
}

// GetOrg serves that landing payload.
func (h *Handlers) GetOrg(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "GetOrg: resolve", err)
		return
	}

	services, err := sqlc.New(h.pool).ListBookableServicesByOrg(r.Context(), oc.row.ID)
	if err != nil {
		h.fail(w, "GetOrg: services", err)
		return
	}

	// Only active queues. A closed line still answers on its own URL — a
	// display should be able to say "closed" rather than go blank — but
	// offering one on a list of places to join is offering a door that is
	// locked.
	queues, err := queue.ListActive(r.Context(), h.pool, oc.id())
	if err != nil {
		h.fail(w, "GetOrg: queues", err)
		return
	}

	summaries := make([]QueueSummary, 0, len(queues))
	for _, q := range queues {
		board, err := queue.LoadBoard(r.Context(), h.pool, q, oc.day)
		if err != nil {
			h.fail(w, "GetOrg: board", err)
			return
		}
		summaries = append(summaries, NewQueueSummary(board))
	}

	h.writeJSON(w, http.StatusOK, OrgSummary{
		Org:      NewOrg(oc.row, oc.now),
		Services: NewServices(services),
		Queues:   summaries,
	})
}

// BoardResponse is one queue's live state, the payload a board, a display
// and a lobby television all render.
//
// It is a superset of the existing /api/q/{org}/{queue} payload rather
// than a replacement for it: that URL is published, businesses have
// embedded it, and breaking it to tidy a route is not a trade this app
// gets to make. What is added here is the waiting numbers themselves —
// numbers only, never names — because a board that shows "now serving 41"
// and nothing else cannot tell a customer holding 44 that they are close.
type BoardResponse struct {
	Queue queue.PublicStatus `json:"queue"`
	// WaitingNumbers is the line as a display draws it: the numbers still
	// waiting, lowest first. It is the one piece of the board a machine
	// could not previously reconstruct from the summary counts.
	WaitingNumbers []int32 `json:"waiting_numbers"`
	// InProgressNumbers are the numbers at a counter, so a screen can
	// show several at once rather than only the lowest.
	InProgressNumbers []int32 `json:"in_progress_numbers"`
}

// GetBoard serves one queue's board.
func (h *Handlers) GetBoard(w http.ResponseWriter, r *http.Request) {
	oc, board, ok := h.resolveBoard(w, r)
	if !ok {
		return
	}

	// The same cache window the published board endpoint uses, for the
	// same reason: a queue moves in minutes and a building full of screens
	// should not each cost a query every second.
	w.Header().Set("Cache-Control", queue.APICacheControl)
	h.writeJSON(w, http.StatusOK, newBoardResponse(oc, board))
}

func newBoardResponse(oc orgContext, board queue.Board) BoardResponse {
	out := BoardResponse{
		Queue:             queue.NewPublicStatus(oc.row, board, oc.now),
		WaitingNumbers:    make([]int32, 0, len(board.Waiting)),
		InProgressNumbers: make([]int32, 0, len(board.InProgress)),
	}
	for _, t := range board.Waiting {
		out.WaitingNumbers = append(out.WaitingNumbers, t.Number)
	}
	for _, t := range board.InProgress {
		out.InProgressNumbers = append(out.InProgressNumbers, t.Number)
	}
	return out
}

// JoinRequest is the walk-in form as JSON.
type JoinRequest struct {
	CustomerName  string `json:"customer_name"`
	CustomerPhone string `json:"customer_phone"`
}

// PostTicket issues a ticket to a walk-in.
//
// It answers with the ticket rather than redirecting, because the caller
// is a front end holding a URL it can route to rather than a browser
// following a Location header. The reason the HTML version redirects — so
// a refresh re-reads the ticket instead of taking a second number — is
// handled on the front end by routing to the ticket's own URL, which is
// what the returned ID is for.
func (h *Handlers) PostTicket(w http.ResponseWriter, r *http.Request) {
	oc, queueRow, ok := h.resolveQueue(w, r)
	if !ok {
		return
	}

	var in JoinRequest
	if err := decodeJSON(w, r, &in); err != nil {
		h.badRequest(w)
		return
	}

	ticket, err := queue.Issue(r.Context(), h.pool, oc.id(), db.UUIDString(queueRow.ID), oc.day, queue.TicketInput{
		CustomerName:  in.CustomerName,
		CustomerPhone: in.CustomerPhone,
	})
	if err != nil {
		h.fail(w, "PostTicket: issue", err)
		return
	}

	// The board is re-read after the write so the ticket's position counts
	// the ticket itself. Rendering against the board loaded before the
	// insert would tell the customer they are one place further forward
	// than they are, every single time.
	board, err := queue.LoadBoard(r.Context(), h.pool, queueRow, oc.day)
	if err != nil {
		h.fail(w, "PostTicket: board", err)
		return
	}

	h.writeJSON(w, http.StatusCreated, NewTicket(ticket, board, oc.loc))
}

// GetTicket serves a customer their own number.
func (h *Handlers) GetTicket(w http.ResponseWriter, r *http.Request) {
	oc, queueRow, ok := h.resolveQueue(w, r)
	if !ok {
		return
	}

	ticket, err := queue.GetTicketPublic(r.Context(), h.pool, db.UUIDString(queueRow.ID), r.PathValue("ticketID"))
	if err != nil {
		h.fail(w, "GetTicket: ticket", err)
		return
	}

	// The ticket's own service day, not today's: a link reopened tomorrow
	// morning must not report a position in a line it was never part of.
	board, err := queue.LoadBoard(r.Context(), h.pool, queueRow, db.DateTime(ticket.TicketDate, oc.loc))
	if err != nil {
		h.fail(w, "GetTicket: board", err)
		return
	}

	h.writeJSON(w, http.StatusOK, NewTicket(ticket, board, oc.loc))
}

// PostTicketCancel withdraws a ticket on its holder's behalf.
//
// A ticket that has already moved on answers with the ticket rather than
// an error, exactly as the HTML page does. The customer's next question is
// "so what is my number doing now", and refusing to answer it because a
// counter got there first is worse than answering it.
func (h *Handlers) PostTicketCancel(w http.ResponseWriter, r *http.Request) {
	oc, queueRow, ok := h.resolveQueue(w, r)
	if !ok {
		return
	}

	queueID := db.UUIDString(queueRow.ID)
	ticketID := r.PathValue("ticketID")

	if _, err := queue.CancelByCustomer(r.Context(), h.pool, queueID, ticketID); err != nil {
		if !isAlreadyResolved(err) {
			h.fail(w, "PostTicketCancel", err)
			return
		}
	}

	// Re-read rather than returning what the cancel produced, so the reply
	// is the ticket as it now stands whether this request changed it or
	// found it already changed.
	ticket, err := queue.GetTicketPublic(r.Context(), h.pool, queueID, ticketID)
	if err != nil {
		h.fail(w, "PostTicketCancel: reread", err)
		return
	}
	board, err := queue.LoadBoard(r.Context(), h.pool, queueRow, db.DateTime(ticket.TicketDate, oc.loc))
	if err != nil {
		h.fail(w, "PostTicketCancel: board", err)
		return
	}

	h.writeJSON(w, http.StatusOK, NewTicket(ticket, board, oc.loc))
}

// isAlreadyResolved reports whether a cancellation failed because the
// ticket had already stopped waiting — which is a state to report, not an
// error to raise.
func isAlreadyResolved(err error) bool {
	return errors.Is(err, queue.ErrNotCancellable)
}

// resolveQueue resolves {orgSlug}/{queueSlug} together. Both segments
// failing identically is the point: a caller must not be able to tell
// "no such business" from "no such line at this business".
func (h *Handlers) resolveQueue(w http.ResponseWriter, r *http.Request) (orgContext, sqlc.Queue, bool) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "resolveQueue: org", err)
		return orgContext{}, sqlc.Queue{}, false
	}

	q, err := queue.GetBySlug(r.Context(), h.pool, oc.id(), r.PathValue("queueSlug"))
	if err != nil {
		h.fail(w, "resolveQueue: queue", err)
		return orgContext{}, sqlc.Queue{}, false
	}
	return oc, q, true
}

// resolveBoard is the shared prologue of every board read.
func (h *Handlers) resolveBoard(w http.ResponseWriter, r *http.Request) (orgContext, queue.Board, bool) {
	oc, q, ok := h.resolveQueue(w, r)
	if !ok {
		return orgContext{}, queue.Board{}, false
	}

	board, err := queue.LoadBoard(r.Context(), h.pool, q, oc.day)
	if err != nil {
		h.fail(w, "resolveBoard: load", err)
		return orgContext{}, queue.Board{}, false
	}
	return oc, board, true
}
