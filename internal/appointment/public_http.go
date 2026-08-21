package appointment

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

// publicContext is a resolved public booking URL: which org, and what
// dates and times mean for that business.
//
// It mirrors queue.publicContext deliberately. Both are the same rule with
// a different tenant source: everything below this point is scoped by the
// org ID resolved here, never by anything else the request carries.
type publicContext struct {
	orgRow sqlc.Organization
	loc    *time.Location
	now    time.Time
}

func (pc publicContext) orgID() string { return db.UUIDString(pc.orgRow.ID) }

func (h *Handlers) resolvePublicOrg(ctx context.Context, orgSlug string) (publicContext, error) {
	orgRow, err := org.BySlug(ctx, h.pool, orgSlug)
	if err != nil {
		return publicContext{}, err
	}
	loc := org.LocationOf(orgRow)
	return publicContext{orgRow: orgRow, loc: loc, now: time.Now().In(loc)}, nil
}

// GetPublicServices lists what a customer can book. It is the page a
// business links to from its own site or a QR code, and the entry point
// every other public booking URL is reached from.
func (h *Handlers) GetPublicServices(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublicOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicServices")
		return
	}

	services, err := ListBookableServices(r.Context(), h.pool, pc.orgID())
	if err != nil {
		h.fail(w, "GetPublicServices: list", err)
		return
	}

	render(w, r, PublicServicesPage(pc.orgRow, services))
}

// GetPublicBook renders the slot picker for one service on one day.
//
// The date is a query parameter rather than a path segment because it is
// navigation, not identity: the page is the same page whichever day it is
// showing, and a link that loses the parameter should land on the first
// day worth offering rather than on a 404.
func (h *Handlers) GetPublicBook(w http.ResponseWriter, r *http.Request) {
	pc, svc, ok := h.publicService(w, r, "GetPublicBook")
	if !ok {
		return
	}

	day := pc.bookableDay(r.URL.Query().Get("date"))
	slots, err := PublicSlots(r.Context(), h.pool, pc.orgID(), db.UUIDString(svc.ID), day, pc.now)
	if err != nil {
		h.publicError(w, r, err, "GetPublicBook: slots")
		return
	}

	render(w, r, PublicBookPage(pc.orgRow, svc, day, slots, pc.loc, ""))
}

// PostPublicBook takes the booking and sends the customer to their
// confirmation page.
//
// It redirects rather than rendering the confirmation inline, for the same
// reason the queue's join form does: a refresh then re-reads the booking
// that exists instead of re-submitting the form and making a second one.
func (h *Handlers) PostPublicBook(w http.ResponseWriter, r *http.Request) {
	pc, svc, resolved := h.publicService(w, r, "PostPublicBook")
	if !resolved {
		return
	}

	// One radio button carries the chosen time and the staff member the
	// page assigned to it. Parsing in the org's timezone rather than the
	// server's is what keeps "10:00" meaning 10:00 where the business is.
	startsAt, staffUserID, ok := ParseSlotToken(r.FormValue("slot"), pc.loc)
	if !ok {
		// A missing or malformed value is the ordinary "they pressed book
		// without choosing a time" case, so it is answered with the form
		// again rather than with a 400 they have to back out of.
		h.rerenderBooking(w, r, pc, svc, "Please choose a time.")
		return
	}

	in := PublicBookInput{
		ServiceID:     db.UUIDString(svc.ID),
		StaffUserID:   staffUserID,
		StartsAt:      startsAt,
		CustomerName:  r.FormValue("customer_name"),
		CustomerEmail: r.FormValue("customer_email"),
		CustomerPhone: r.FormValue("customer_phone"),
	}

	appt, err := BookPublic(r.Context(), h.pool, pc.orgID(), in, pc.now)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.rerenderBooking(w, r, pc, svc, h.publicBookingError(err))
		return
	}

	http.Redirect(w, r, appointmentURL(pc.orgRow.Slug, db.UUIDString(appt.ID)), http.StatusSeeOther)
}

// GetPublicAppointmentPage is the customer's own copy of the booking: when
// it is, who they are seeing, and the button that releases it.
//
// It carries no session. The tenant comes from the org slug and the
// booking from an unguessable UUID, which is the same standard the public
// ticket page holds — a link is the credential, and the page shows only
// what the person holding it already submitted.
func (h *Handlers) GetPublicAppointmentPage(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublicOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicAppointmentPage")
		return
	}

	appt, err := GetPublicAppointment(r.Context(), h.pool, pc.orgID(), r.PathValue("appointmentID"))
	if err != nil {
		h.publicError(w, r, err, "GetPublicAppointmentPage: appointment")
		return
	}

	render(w, r, PublicAppointmentPage(pc.orgRow, appt, pc.loc, pc.now, ""))
}

// PostPublicCancel releases a booking on its holder's behalf.
//
// Like the queue's "leave the line" this is an unauthenticated write with
// no CSRF token, because a customer following a link from their own email
// has neither a session nor a token. The booking's random ID stands in for
// one: a forged cross-site POST would have to name the appointment it
// wants to cancel, and anyone holding that ID can already read the page.
func (h *Handlers) PostPublicCancel(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolvePublicOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.publicError(w, r, err, "PostPublicCancel")
		return
	}

	appointmentID := r.PathValue("appointmentID")
	_, err = CancelPublic(r.Context(), h.pool, pc.orgID(), appointmentID, pc.now)
	switch {
	case err == nil, errors.Is(err, ErrInvalidTransition):
		// Already cancelled, or the business resolved it first. Either
		// way the customer's next question is what their booking is doing
		// now, and the page below answers it — better than an error page
		// about a booking that has already moved on.
	case errors.Is(err, pgx.ErrNoRows):
		http.NotFound(w, r)
		return
	case errors.Is(err, ErrTooLateToCancel):
		appt, gerr := GetPublicAppointment(r.Context(), h.pool, pc.orgID(), appointmentID)
		if gerr != nil {
			h.publicError(w, r, gerr, "PostPublicCancel: appointment")
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, PublicAppointmentPage(pc.orgRow, appt, pc.loc, pc.now,
			"This appointment has already started — please call us instead."))
		return
	default:
		h.fail(w, "PostPublicCancel", err)
		return
	}

	http.Redirect(w, r, appointmentURL(pc.orgRow.Slug, appointmentID), http.StatusSeeOther)
}

// publicService resolves the {orgSlug}/{serviceSlug} pair both booking
// routes start from. An unknown org, an unknown service and an inactive
// one all fail identically, so probing the URL space says nothing about
// who uses the product or what they sell.
func (h *Handlers) publicService(w http.ResponseWriter, r *http.Request, op string) (publicContext, sqlc.Service, bool) {
	pc, err := h.resolvePublicOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.publicError(w, r, err, op)
		return publicContext{}, sqlc.Service{}, false
	}

	svc, err := sqlc.New(h.pool).GetServiceBySlug(r.Context(), sqlc.GetServiceBySlugParams{
		OrgID: pc.orgRow.ID,
		Slug:  r.PathValue("serviceSlug"),
	})
	if err != nil {
		h.publicError(w, r, err, op+": service")
		return publicContext{}, sqlc.Service{}, false
	}
	if !svc.Active {
		http.NotFound(w, r)
		return publicContext{}, sqlc.Service{}, false
	}
	return pc, svc, true
}

// rerenderBooking answers a failed submission with the picker again,
// re-read rather than redrawn from the request: whatever went wrong,
// the times on the page must be the ones still available now.
func (h *Handlers) rerenderBooking(w http.ResponseWriter, r *http.Request, pc publicContext, svc sqlc.Service, msg string) {
	day := pc.bookableDay(r.FormValue("date"))
	slots, err := PublicSlots(r.Context(), h.pool, pc.orgID(), db.UUIDString(svc.ID), day, pc.now)
	if err != nil {
		h.publicError(w, r, err, "rerenderBooking: slots")
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	render(w, r, PublicBookPage(pc.orgRow, svc, day, slots, pc.loc, msg))
}

// bookableDay reads the requested date and clamps it into the window the
// booking rules allow.
//
// Clamping rather than rejecting is deliberate: the parameter is a
// navigation convenience, and someone who follows a stale link or edits
// the URL should land on the nearest day they could actually book instead
// of on an error. Today is the near edge even when every slot on it has
// passed — a customer opening the page at 17:00 should see today's empty
// list and step forward from it, not be moved to tomorrow without being
// told why.
func (pc publicContext) bookableDay(value string) time.Time {
	today := startOfDay(pc.now)
	last := startOfDay(pc.now.Add(MaxBookAhead))

	day := parseDay(value, pc.loc)
	if day.Before(today) {
		return today
	}
	if day.After(last) {
		return last
	}
	return day
}

// publicBookingError turns a domain error into something a customer can
// act on. It is separate from the admin-facing userFacingError because the
// same failure needs different words: staff are told a slot was taken, a
// customer is told to pick another time.
func (h *Handlers) publicBookingError(err error) string {
	var verr ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, ErrTooSoon):
		return "That time is too soon to book online. Please choose a later one, or call us."
	case errors.Is(err, ErrTooFarAhead):
		return "We're not taking bookings that far ahead yet."
	case errors.Is(err, ErrSlotNotOffered), errors.Is(err, ErrSlotTaken), errors.Is(err, ErrStaffNotAssigned):
		// All three mean the same thing to the person reading it: the
		// time they picked is gone, and the list below is current.
		return "Sorry — that time has just been taken. Please choose another."
	default:
		h.log.Error("public booking error", "error", err)
		return "Something went wrong. Please try again."
	}
}

func (h *Handlers) publicError(w http.ResponseWriter, r *http.Request, err error, op string) {
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

func bookURL(orgSlug, serviceSlug string) string {
	return "/book/" + orgSlug + "/" + serviceSlug
}

func appointmentURL(orgSlug, appointmentID string) string {
	return "/book/" + orgSlug + "/a/" + appointmentID
}
