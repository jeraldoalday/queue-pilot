package queue

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/platform/httpx"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type Handlers struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewHandlers(pool *pgxpool.Pool, log *slog.Logger) *Handlers {
	return &Handlers{pool: pool, log: log}
}

// GetQueues renders the queue admin page: every line the org has, open or
// closed, with its public board link.
func (h *Handlers) GetQueues(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queues, err := List(r.Context(), h.pool, orgID)
	if err != nil {
		h.fail(w, "GetQueues: list", err)
		return
	}
	orgRow, err := sqlc.New(h.pool).GetOrganizationByID(r.Context(), db.MustUUID(orgID))
	if err != nil {
		h.fail(w, "GetQueues: org", err)
		return
	}

	render(w, r, QueuesPage(queues, orgRow.Slug, "", ident.CSRFToken))
}

func (h *Handlers) PostQueues(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	if _, err := Create(r.Context(), h.pool, orgID, Input{Name: r.FormValue("name")}); err != nil {
		h.renderQueueSection(w, r, orgID, ident.CSRFToken, h.userFacingError(err), http.StatusUnprocessableEntity)
		return
	}
	h.renderQueueSection(w, r, orgID, ident.CSRFToken, "", http.StatusOK)
}

// PostQueue renames a queue and/or opens and closes it. Closing is the
// reversible action the UI leads with; the checkbox and the name field
// post together so one save can do both.
func (h *Handlers) PostQueue(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	in := Input{
		Name:   r.FormValue("name"),
		Active: r.FormValue("active") != "",
	}
	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := Update(r.Context(), h.pool, orgID, queueID, in); err != nil {
		h.renderQueueSection(w, r, orgID, ident.CSRFToken, h.userFacingError(err), http.StatusUnprocessableEntity)
		return
	}
	h.renderQueueSection(w, r, orgID, ident.CSRFToken, "", http.StatusOK)
}

func (h *Handlers) DeleteQueue(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	if err := Delete(r.Context(), h.pool, orgID, queueID); err != nil {
		h.renderQueueSection(w, r, orgID, ident.CSRFToken,
			"Couldn't delete that queue. Please try again.", http.StatusUnprocessableEntity)
		return
	}
	h.renderQueueSection(w, r, orgID, ident.CSRFToken, "", http.StatusOK)
}

// GetConsole renders the staff console for one queue: the live board plus
// the actions that move tickets through it.
func (h *Handlers) GetConsole(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	board, _, err := h.board(r.Context(), orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "GetConsole")
		return
	}
	render(w, r, ConsolePage(board, "", ident.CSRFToken))
}

// GetConsoleBoard serves the console's polled refresh. The console is a
// shared screen — one member of staff calling a ticket has to show up on
// every other counter's console without them reloading — so the board
// re-fetches itself on a timer rather than only after this browser acts.
func (h *Handlers) GetConsoleBoard(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	board, _, err := h.board(r.Context(), orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "GetConsoleBoard")
		return
	}
	render(w, r, ConsoleBoard(board, "", ident.CSRFToken))
}

// PostCallNext hands the caller the next waiting customer.
func (h *Handlers) PostCallNext(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}
	queue, day, err := h.queueAndDay(r.Context(), orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "PostCallNext")
		return
	}

	msg := ""
	if _, err := CallNext(r.Context(), h.pool, orgID, queueID, ident.UserID, day); err != nil {
		if !errors.Is(err, ErrNoWaitingTickets) {
			h.fail(w, "PostCallNext", err)
			return
		}
		// An empty line is an ordinary outcome, not an error state.
		msg = "Nobody is waiting right now."
	}

	h.renderBoard(w, r, queue, day, msg, ident.CSRFToken)
}

// PostTicket issues a ticket from the counter, for a walk-in who asks a
// member of staff rather than using the kiosk.
func (h *Handlers) PostTicket(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}
	queue, day, err := h.queueAndDay(r.Context(), orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "PostTicket")
		return
	}

	in := TicketInput{
		CustomerName:  r.FormValue("customer_name"),
		CustomerPhone: r.FormValue("customer_phone"),
	}
	msg := ""
	if _, err := Issue(r.Context(), h.pool, orgID, queueID, day, in); err != nil {
		msg = h.userFacingError(err)
	}

	h.renderBoard(w, r, queue, day, msg, ident.CSRFToken)
}

// PostTicketStatus applies one lifecycle transition and re-renders the
// board, so the actions offered on a ticket always match its new state.
func (h *Handlers) PostTicketStatus(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	ticketID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	to, valid := ParseStatus(r.FormValue("status"))
	if !valid {
		http.Error(w, "unknown status", http.StatusBadRequest)
		return
	}

	ticket, err := SetStatus(r.Context(), h.pool, orgID, ticketID, to, ident.UserID)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			http.NotFound(w, r)
		case errors.Is(err, ErrInvalidTransition):
			// Two counters acting on the same ticket is the common cause,
			// so this is a conflict rather than a bad request.
			http.Error(w, "that ticket can no longer change status", http.StatusConflict)
		default:
			h.fail(w, "PostTicketStatus", err)
		}
		return
	}

	// Re-render the ticket's own queue and service day, not whatever the
	// page happened to be showing, so the list stays truthful.
	queue, err := Get(r.Context(), h.pool, orgID, db.UUIDString(ticket.QueueID))
	if err != nil {
		h.fail(w, "PostTicketStatus: queue", err)
		return
	}
	loc, err := org.Location(r.Context(), h.pool, orgID)
	if err != nil {
		h.fail(w, "PostTicketStatus: org location", err)
		return
	}

	h.renderBoard(w, r, queue, db.DateTime(ticket.TicketDate, loc), "", ident.CSRFToken)
}

// board loads a queue and its board for the org's current service day.
func (h *Handlers) board(ctx context.Context, orgID, queueID string) (Board, *time.Location, error) {
	loc, err := org.Location(ctx, h.pool, orgID)
	if err != nil {
		return Board{}, nil, err
	}
	queue, err := Get(ctx, h.pool, orgID, queueID)
	if err != nil {
		return Board{}, nil, err
	}
	board, err := LoadBoard(ctx, h.pool, queue, today(loc))
	return board, loc, err
}

func (h *Handlers) queueAndDay(ctx context.Context, orgID, queueID string) (sqlc.Queue, time.Time, error) {
	loc, err := org.Location(ctx, h.pool, orgID)
	if err != nil {
		return sqlc.Queue{}, time.Time{}, err
	}
	queue, err := Get(ctx, h.pool, orgID, queueID)
	if err != nil {
		return sqlc.Queue{}, time.Time{}, err
	}
	return queue, today(loc), nil
}

func (h *Handlers) renderBoard(w http.ResponseWriter, r *http.Request, queue sqlc.Queue, day time.Time, msg, csrfToken string) {
	board, err := LoadBoard(r.Context(), h.pool, queue, day)
	if err != nil {
		h.fail(w, "renderBoard", err)
		return
	}
	render(w, r, ConsoleBoard(board, msg, csrfToken))
}

func (h *Handlers) renderQueueSection(w http.ResponseWriter, r *http.Request, orgID, csrfToken, errMsg string, code int) {
	queues, err := List(r.Context(), h.pool, orgID)
	if err != nil {
		h.fail(w, "renderQueueSection: list", err)
		return
	}
	orgRow, err := sqlc.New(h.pool).GetOrganizationByID(r.Context(), db.MustUUID(orgID))
	if err != nil {
		h.fail(w, "renderQueueSection: org", err)
		return
	}
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	render(w, r, QueuesSection(queues, orgRow.Slug, errMsg, csrfToken))
}

func (h *Handlers) orgID(w http.ResponseWriter, r *http.Request) (string, bool) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	return orgID, true
}

// boardError distinguishes an unknown or other-org queue ID (a 404) from a
// genuine failure, so a guessed ID leaks nothing beyond "no such page".
func (h *Handlers) boardError(w http.ResponseWriter, r *http.Request, err error, op string) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	h.fail(w, op, err)
}

func (h *Handlers) fail(w http.ResponseWriter, op string, err error) {
	h.log.Error(op, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handlers) userFacingError(err error) string {
	var verr ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, ErrSlugTaken):
		return "A queue with a conflicting name already exists."
	case errors.Is(err, ErrQueueClosed):
		return "This queue isn't accepting walk-ins right now."
	case errors.Is(err, ErrNoWaitingTickets):
		return "Nobody is waiting right now."
	default:
		h.log.Error("queue handler error", "error", err)
		return "Something went wrong. Please try again."
	}
}

// today is the current service day in the org's timezone. A queue's day
// must turn over when the business's day does, not when UTC's does.
func today(loc *time.Location) time.Time {
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
