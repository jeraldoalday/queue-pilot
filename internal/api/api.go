// Package api serves QueuePilot's public surface as JSON.
//
// The server-rendered pages under /q and /book remain the product: they
// need no build step, they work with scripting switched off, and a shop
// can print a QR code that points at one. This package exists beside them
// because the same three things a customer does — read a board, take a
// number, book a time — are also things a *front end* wants to do, and a
// front end cannot do them against HTML without scraping it.
//
// Everything here follows the rules the public pages already follow, and
// for the same reasons:
//
//   - the tenant comes from the {orgSlug} path segment, never from a
//     client-supplied field, and every lookup below it is scoped by the
//     org ID that slug resolves to;
//   - a missing org, a missing queue and one belonging to another org all
//     answer 404 identically, so probing the URL space does not become a
//     directory of who uses the product;
//   - a customer's own ticket or appointment is reached by an unguessable
//     UUID and scoped by the queue or org around it, which is the same
//     pair that already gates the HTML page for it;
//   - the board payload carries no names or phone numbers, so it is safe
//     to serve to any origin without a session.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/queue"
)

type Handlers struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// now is the clock, injected so the booking window's boundaries are
	// testable. Production passes time.Now.
	now func() time.Time
}

func NewHandlers(pool *pgxpool.Pool, log *slog.Logger) *Handlers {
	return &Handlers{pool: pool, log: log, now: time.Now}
}

// Error is the single shape every failure takes.
//
// The code is for the front end to branch on and the message is for a
// person to read. Keeping both means the UI can say "that time was just
// taken, here are the times that are left" without matching on English
// prose, and a caller that knows no codes still has something to show.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

// The codes. They are a closed set on purpose: an endpoint returning a
// code the front end has never heard of is indistinguishable from a bug,
// so a new failure mode should be named here rather than invented at a
// call site.
const (
	// CodeNotFound covers every "no such thing" — including a thing that
	// exists but belongs to somebody else, which must not be
	// distinguishable from one that does not exist at all.
	CodeNotFound = "not_found"
	// CodeValidation is a field the customer can fix by retyping it.
	CodeValidation = "validation"
	// CodeQueueClosed means the line is real but shut. It is separate from
	// not_found because the answer is "come back tomorrow", not "check
	// the address".
	CodeQueueClosed = "queue_closed"
	// CodeSlotTaken means somebody else booked the time between the page
	// rendering and the form arriving. The front end's job is to re-fetch
	// the day rather than to make the customer start over.
	CodeSlotTaken = "slot_taken"
	// CodeTooSoon and CodeTooFarAhead are the two ends of the booking
	// window, named separately so the message can say which one.
	CodeTooSoon     = "too_soon"
	CodeTooFarAhead = "too_far_ahead"
	// CodeNotCancellable is a ticket or appointment past the point where
	// its holder can withdraw it.
	CodeNotCancellable = "not_cancellable"
	// CodeRateLimited mirrors the limit the public forms already carry.
	CodeRateLimited = "rate_limited"
	// CodeInternal is the only code that does not describe what went
	// wrong, because the only honest thing to tell a customer about a
	// failed query is that it failed.
	CodeInternal = "internal"
)

// writeJSON sends a value with the headers every response here shares.
//
// The status is written before the body, so an encoding failure partway
// through cannot be turned into a 500 — the header is already gone. That
// is logged rather than swallowed: the caller gets truncated JSON either
// way, and the log is what makes it visible.
func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log.Error("api: encode response", "error", err, "status", status)
	}
}

// writeError sends a failure in the one shape callers parse.
func (h *Handlers) writeError(w http.ResponseWriter, status int, code, message string) {
	h.writeJSON(w, status, errorEnvelope{Error{Code: code, Message: message}})
}

// fail maps a domain error onto the wire.
//
// Every handler funnels its errors through here so the mapping lives in
// one place. The default arm is deliberate: an error this function has not
// been taught about becomes a logged 500 with a generic message, never a
// database error forwarded to a customer. A message is only ever shown
// verbatim when the domain marked it as safe to show — the ValidationError
// types exist for exactly that.
func (h *Handlers) fail(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		h.notFound(w)

	case errors.Is(err, queue.ErrQueueClosed):
		h.writeError(w, http.StatusConflict, CodeQueueClosed, "This line is not accepting walk-ins right now.")

	case errors.Is(err, queue.ErrNotCancellable):
		h.writeError(w, http.StatusConflict, CodeNotCancellable, "This ticket can no longer be cancelled.")

	case errors.Is(err, appointment.ErrTooLateToCancel):
		h.writeError(w, http.StatusConflict, CodeNotCancellable, "This appointment has already started.")

	case errors.Is(err, appointment.ErrTooSoon):
		h.writeError(w, http.StatusConflict, CodeTooSoon, "That time is too soon to book online. Please pick a later one.")

	case errors.Is(err, appointment.ErrTooFarAhead):
		h.writeError(w, http.StatusConflict, CodeTooFarAhead, "That date is further ahead than online booking goes.")

	case errors.Is(err, appointment.ErrSlotTaken), errors.Is(err, appointment.ErrSlotNotOffered):
		// Both reach a customer as the same fact: the time they picked is
		// not bookable any more. Which of the two it was is a distinction
		// between "somebody beat you to it" and "your page is stale", and
		// the customer does the same thing either way.
		h.writeError(w, http.StatusConflict, CodeSlotTaken, "That time is no longer available. Please choose another.")

	default:
		var qv queue.ValidationError
		if errors.As(err, &qv) {
			h.writeError(w, http.StatusUnprocessableEntity, CodeValidation, qv.Error())
			return
		}
		var av appointment.ValidationError
		if errors.As(err, &av) {
			h.writeError(w, http.StatusUnprocessableEntity, CodeValidation, av.Error())
			return
		}

		h.log.Error("api: "+op, "error", err)
		h.writeError(w, http.StatusInternalServerError, CodeInternal, "Something went wrong. Please try again.")
	}
}

// notFound is the single 404. Its message says nothing about which part of
// the path failed to resolve, because the org, the queue and the ticket
// all have to fail identically for the URL space to stay opaque.
func (h *Handlers) notFound(w http.ResponseWriter) {
	h.writeError(w, http.StatusNotFound, CodeNotFound, "Not found.")
}

// decodeJSON reads a request body into v, refusing anything that is not
// the object the endpoint expects.
//
// Unknown fields are rejected rather than ignored. A front end sending
// customerName where the API wants customer_name would otherwise book an
// appointment for nobody and find out from a customer, and a typo in a
// field name is much easier to diagnose at the boundary that saw it.
//
// A body is capped before it is parsed, because these endpoints are
// unauthenticated: without a limit, a request that never ends is a request
// that holds a connection and a decoder buffer for as long as it likes.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// maxBodyBytes bounds a request body. Every payload here is a handful of
// short strings — a name, a phone number, a slot token — so 16 KiB is
// several orders of magnitude more than any honest caller sends.
const maxBodyBytes = 16 << 10

// badRequest answers a body that could not be read at all. It is a
// validation failure from the caller's side, and the message avoids
// quoting the decoder: "invalid character 'x' looking for beginning of
// value" is not something to show a customer.
func (h *Handlers) badRequest(w http.ResponseWriter) {
	h.writeError(w, http.StatusBadRequest, CodeValidation, "That request could not be read.")
}

// RateLimited answers a request the public limiter refused.
//
// It exists so that the one failure produced by middleware rather than by
// a handler still arrives in the shape this API promises. The rest of the
// closed code set is useless to a front end if the response it is most
// likely to meet under load — everybody pressing "take a number" at
// nine in the morning — is the one that arrives as plain text and has to
// be recognised from a bare status code.
//
// The wait is repeated in the message because Retry-After is a header a
// front end has to go looking for, and a customer reads the sentence.
func (h *Handlers) RateLimited(w http.ResponseWriter, _ *http.Request, retryAfterSeconds int) {
	message := "That was a lot of requests at once. Please try again in a few seconds."
	if retryAfterSeconds > 1 {
		message = "That was a lot of requests at once. Please try again in " +
			strconv.Itoa(retryAfterSeconds) + " seconds."
	}
	h.writeError(w, http.StatusTooManyRequests, CodeRateLimited, message)
}

// NotFound answers a path under /api/v1/ that no route matched.
//
// It exists so the API's 404 is JSON. Falling through to the HTML router's
// http.NotFound would hand a front end a page where it expected an error
// envelope, and the first thing that front end does with a body it cannot
// parse is report the wrong failure.
func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	h.notFound(w)
}
