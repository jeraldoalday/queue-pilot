package queue

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
)

// publicContext is a resolved public URL: which org, which queue, and what
// "today" means for that business.
//
// Everything a public page reads is scoped by orgID and queue.ID from
// here, never by anything else in the request. That is the same rule the
// authenticated side follows — the only difference is where the tenant
// comes from (a URL slug rather than a session).
type publicContext struct {
	orgRow sqlc.Organization
	queue  sqlc.Queue
	loc    *time.Location
	day    time.Time
}

// resolvePublic turns the {orgSlug}/{queueSlug} path segments into a
// publicContext. An unknown org, an unknown queue, or a queue belonging to
// a different org all fail identically with pgx.ErrNoRows, so probing the
// URL space reveals nothing about which orgs exist.
func (h *Handlers) resolvePublic(ctx context.Context, orgSlug, queueSlug string) (publicContext, error) {
	orgRow, err := org.BySlug(ctx, h.pool, orgSlug)
	if err != nil {
		return publicContext{}, err
	}

	orgID := db.UUIDString(orgRow.ID)
	q, err := GetBySlug(ctx, h.pool, orgID, queueSlug)
	if err != nil {
		return publicContext{}, err
	}

	loc := org.LocationOf(orgRow)
	return publicContext{orgRow: orgRow, queue: q, loc: loc, day: today(loc)}, nil
}

// GetPublicBoard is the page a customer opens from a QR code: what is
// being served now, how long the line is, and a form to join it.
func (h *Handlers) GetPublicBoard(w http.ResponseWriter, r *http.Request) {
	pc, board, ok := h.publicBoard(w, r)
	if !ok {
		return
	}
	render(w, r, PublicBoardPage(pc.orgRow, board, ""))
}

// GetPublicBoardPartial serves the public board's own refresh. A customer
// standing in the room watches this page rather than reloading it, so it
// polls, exactly like the staff console.
func (h *Handlers) GetPublicBoardPartial(w http.ResponseWriter, r *http.Request) {
	pc, board, ok := h.publicBoard(w, r)
	if !ok {
		return
	}
	render(w, r, PublicBoardBody(pc.orgRow, board, ""))
}

// GetPublicDisplay is the waiting-room screen: the same board with the
// now-serving number set large and no join form, meant to be left open on
// a monitor nobody interacts with.
func (h *Handlers) GetPublicDisplay(w http.ResponseWriter, r *http.Request) {
	pc, board, ok := h.publicBoard(w, r)
	if !ok {
		return
	}
	render(w, r, PublicDisplayPage(pc.orgRow, board))
}

func (h *Handlers) GetPublicDisplayPartial(w http.ResponseWriter, r *http.Request) {
	pc, board, ok := h.publicBoard(w, r)
	if !ok {
		return
	}
	render(w, r, PublicDisplayBody(pc.orgRow, board))
}

// PostPublicTicket issues a ticket to a walk-in from the public page and
// sends them to their own ticket page.
//
// It redirects rather than rendering the ticket inline so that a refresh
// (or a phone restoring the tab hours later) re-reads the existing ticket
// instead of re-submitting the form and taking a second number.
func (h *Handlers) PostPublicTicket(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublic(r.Context(), r.PathValue("orgSlug"), r.PathValue("queueSlug"))
	if err != nil {
		h.publicError(w, r, err, "PostPublicTicket")
		return
	}

	in := TicketInput{
		CustomerName:  r.FormValue("customer_name"),
		CustomerPhone: r.FormValue("customer_phone"),
	}

	ticket, err := Issue(r.Context(), h.pool, db.UUIDString(pc.orgRow.ID), db.UUIDString(pc.queue.ID), pc.day, in)
	if err != nil {
		board, berr := LoadBoard(r.Context(), h.pool, pc.queue, pc.day)
		if berr != nil {
			h.fail(w, "PostPublicTicket: board", berr)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, PublicBoardPage(pc.orgRow, board, h.userFacingError(err)))
		return
	}

	http.Redirect(w, r, ticketURL(pc.orgRow.Slug, pc.queue.Slug, db.UUIDString(ticket.ID)), http.StatusSeeOther)
}

// GetPublicTicket is the customer's own page: their number, their place in
// line and what that means in minutes.
func (h *Handlers) GetPublicTicket(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublic(r.Context(), r.PathValue("orgSlug"), r.PathValue("queueSlug"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicTicket")
		return
	}

	ticket, err := GetTicketPublic(r.Context(), h.pool, db.UUIDString(pc.queue.ID), r.PathValue("ticketID"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicTicket: ticket")
		return
	}

	// Read the board for the ticket's own service day, not today's: a
	// customer reopening yesterday's link should see that ticket's day
	// rather than a position in a line it was never part of.
	board, err := LoadBoard(r.Context(), h.pool, pc.queue, db.DateTime(ticket.TicketDate, pc.loc))
	if err != nil {
		h.fail(w, "GetPublicTicket: board", err)
		return
	}

	render(w, r, PublicTicketPage(pc.orgRow, board, ticket))
}

// GetPublicTicketPartial serves the ticket page's poll, so a customer
// watching their phone sees their position fall without refreshing.
func (h *Handlers) GetPublicTicketPartial(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublic(r.Context(), r.PathValue("orgSlug"), r.PathValue("queueSlug"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicTicketPartial")
		return
	}

	ticket, err := GetTicketPublic(r.Context(), h.pool, db.UUIDString(pc.queue.ID), r.PathValue("ticketID"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicTicketPartial: ticket")
		return
	}

	board, err := LoadBoard(r.Context(), h.pool, pc.queue, db.DateTime(ticket.TicketDate, pc.loc))
	if err != nil {
		h.fail(w, "GetPublicTicketPartial: board", err)
		return
	}

	render(w, r, PublicTicketBody(pc.orgRow, board, ticket))
}

// publicBoard is the shared prologue of every public read: resolve the
// URL, load the board for today.
func (h *Handlers) publicBoard(w http.ResponseWriter, r *http.Request) (publicContext, Board, bool) {
	pc, err := h.resolvePublic(r.Context(), r.PathValue("orgSlug"), r.PathValue("queueSlug"))
	if err != nil {
		h.publicError(w, r, err, "publicBoard")
		return publicContext{}, Board{}, false
	}

	board, err := LoadBoard(r.Context(), h.pool, pc.queue, pc.day)
	if err != nil {
		h.fail(w, "publicBoard: load", err)
		return publicContext{}, Board{}, false
	}
	return pc, board, true
}

// publicError maps a missing org, queue or ticket to a plain 404. These
// pages are unauthenticated, so the response must not distinguish "no such
// org" from "no such queue in this org" — otherwise the URL space becomes
// a directory of who uses the product.
func (h *Handlers) publicError(w http.ResponseWriter, r *http.Request, err error, op string) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	h.fail(w, op, err)
}

func ticketURL(orgSlug, queueSlug, ticketID string) string {
	return "/q/" + orgSlug + "/" + queueSlug + "/t/" + ticketID
}
