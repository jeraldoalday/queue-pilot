package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/httpx"
	"github.com/jeraldoalday/queuepilot/internal/platform/icalendar"
)

// SlotsResponse is one service's open times on one date, with the window
// the customer is allowed to pick inside.
//
// The window is sent rather than assumed because the front end draws the
// date picker, and a picker that offers days the booking endpoint will
// refuse produces a customer who tried three times and gave up. Both ends
// come from BookingWindow, which is also what the endpoint validates
// against, so the two cannot disagree.
type SlotsResponse struct {
	Service Service `json:"service"`
	// Date is the day these slots are for, in the org's own calendar.
	Date  string `json:"date"`
	Slots []Slot `json:"slots"`
	// EarliestBookable and LatestBookable bound the picker.
	EarliestBookable time.Time `json:"earliest_bookable"`
	LatestBookable   time.Time `json:"latest_bookable"`
}

// GetSlots serves the times a customer may book for one service on one
// date.
//
// The date comes in as ?date=YYYY-MM-DD and is read in the org's zone, not
// the server's or the caller's. A customer in London asking a Manila
// clinic for "2026-04-02" means the clinic's Thursday, and parsing that
// string anywhere else moves it by a day for half the world.
func (h *Handlers) GetSlots(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "GetSlots: org", err)
		return
	}

	svc, err := h.bookableService(r, oc)
	if err != nil {
		h.fail(w, "GetSlots: service", err)
		return
	}

	date, err := parseDate(r.URL.Query().Get("date"), oc)
	if err != nil {
		h.writeError(w, http.StatusUnprocessableEntity, CodeValidation, "That date could not be read. Use YYYY-MM-DD.")
		return
	}

	open, err := appointment.PublicSlots(r.Context(), h.pool, oc.id(), db.UUIDString(svc.ID), date, oc.now)
	if err != nil {
		h.fail(w, "GetSlots: slots", err)
		return
	}

	earliest, latest := appointment.BookingWindow(oc.now)
	h.writeJSON(w, http.StatusOK, SlotsResponse{
		Service:          NewService(svc),
		Date:             date.Format(serviceDayLayout),
		Slots:            NewSlots(open, oc.loc),
		EarliestBookable: earliest.In(oc.loc),
		LatestBookable:   latest.In(oc.loc),
	})
}

// BookRequest is the public booking form as JSON. The slot token is the
// value the page handed out; everything else the customer typed.
type BookRequest struct {
	Slot          string `json:"slot"`
	CustomerName  string `json:"customer_name"`
	CustomerEmail string `json:"customer_email"`
	CustomerPhone string `json:"customer_phone"`
}

// PostBooking books an appointment from the public page.
//
// The token is decoded here and then thrown away as evidence: BookPublic
// re-derives the day's slots and requires the submitted one to be among
// them. That is what stops an edited request booking a staff member at
// three in the morning, booking a service they do not perform, or taking a
// slot that was offered an hour ago and filled since.
func (h *Handlers) PostBooking(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "PostBooking: org", err)
		return
	}

	svc, err := h.bookableService(r, oc)
	if err != nil {
		h.fail(w, "PostBooking: service", err)
		return
	}

	var in BookRequest
	if err := decodeJSON(w, r, &in); err != nil {
		h.badRequest(w)
		return
	}

	// A malformed token is treated as no choice at all, which is what it
	// amounts to from the customer's side, and answered with the same
	// message choosing nothing would get.
	start, staffUserID, ok := appointment.ParseSlotToken(in.Slot, oc.loc)
	if !ok {
		h.writeError(w, http.StatusUnprocessableEntity, CodeValidation, "Please choose a time.")
		return
	}

	booked, err := appointment.BookPublic(r.Context(), h.pool, oc.id(), appointment.PublicBookInput{
		ServiceID:     db.UUIDString(svc.ID),
		StaffUserID:   staffUserID,
		StartsAt:      start,
		CustomerName:  in.CustomerName,
		CustomerEmail: in.CustomerEmail,
		CustomerPhone: in.CustomerPhone,
	}, oc.now)
	if err != nil {
		h.fail(w, "PostBooking: book", err)
		return
	}

	// Re-read with the service and staff names attached: the confirmation
	// has to say "10:00 with Dana for a trim", and the insert returns
	// neither name.
	full, err := appointment.GetPublicAppointment(r.Context(), h.pool, oc.id(), db.UUIDString(booked.ID))
	if err != nil {
		h.fail(w, "PostBooking: reread", err)
		return
	}

	h.writeJSON(w, http.StatusCreated, NewAppointment(full, oc.now, oc.loc))
}

// GetAppointment serves a customer their own booking, reached by the
// unguessable ID in their confirmation link.
func (h *Handlers) GetAppointment(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "GetAppointment: org", err)
		return
	}

	row, err := appointment.GetPublicAppointment(r.Context(), h.pool, oc.id(), r.PathValue("appointmentID"))
	if err != nil {
		h.fail(w, "GetAppointment", err)
		return
	}

	h.writeJSON(w, http.StatusOK, NewAppointment(row, oc.now, oc.loc))
}

// GetAppointmentCalendar serves the same booking as a file a calendar can
// read, so a front end hosted anywhere can offer "add to calendar" without
// building an iCalendar document of its own.
//
// It is the one endpoint here that does not answer JSON. That is on
// purpose: the value of this route is that its response can be handed
// straight to the operating system, and a front end that had to assemble
// the file itself would be a second implementation of the escaping and
// folding rules — which is exactly how the two ends of a product come to
// disagree about what a customer's appointment says.
//
// A failure still answers with the JSON envelope. A caller that asked for
// a calendar and got an error needs to know which error, and the closed
// set of codes is the only vocabulary this API has for saying so.
func (h *Handlers) GetAppointmentCalendar(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "GetAppointmentCalendar: org", err)
		return
	}

	row, err := appointment.GetPublicAppointment(r.Context(), h.pool, oc.id(), r.PathValue("appointmentID"))
	if err != nil {
		h.fail(w, "GetAppointmentCalendar", err)
		return
	}

	// The event links back to the server-rendered confirmation page rather
	// than to whatever front end asked for the file. That page always
	// exists, needs no build step and works with scripting off, which is
	// what a link inside a calendar entry opened in six weeks needs to be.
	page := httpx.AbsoluteURL(r, "/book/"+oc.row.Slug+"/a/"+db.UUIDString(row.ID))
	cal := appointment.AppointmentCalendar(oc.row, row, page)

	w.Header().Set("Content-Type", icalendar.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+appointment.CalendarFilename(oc.row, row, oc.loc)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(cal.Bytes(oc.now)); err != nil {
		h.log.Warn("api: GetAppointmentCalendar: write", "error", err)
	}
}

// PostAppointmentCancel releases a booking on its holder's behalf.
//
// Unlike a ticket, a refused cancellation is reported rather than
// swallowed. A ticket that has moved on has moved on regardless — the
// customer's number is past — but an appointment that has already started
// is one the customer may still be expected at, and telling them "cancelled"
// when it was not is how somebody stays home from an appointment nobody
// released.
func (h *Handlers) PostAppointmentCancel(w http.ResponseWriter, r *http.Request) {
	oc, err := h.resolveOrg(r.Context(), r.PathValue("orgSlug"))
	if err != nil {
		h.fail(w, "PostAppointmentCancel: org", err)
		return
	}

	appointmentID := r.PathValue("appointmentID")
	if _, err := appointment.CancelPublic(r.Context(), h.pool, oc.id(), appointmentID, oc.now); err != nil {
		h.fail(w, "PostAppointmentCancel", err)
		return
	}

	row, err := appointment.GetPublicAppointment(r.Context(), h.pool, oc.id(), appointmentID)
	if err != nil {
		h.fail(w, "PostAppointmentCancel: reread", err)
		return
	}

	h.writeJSON(w, http.StatusOK, NewAppointment(row, oc.now, oc.loc))
}

// bookableService resolves the {serviceSlug} segment, refusing anything
// the public list would not have shown.
//
// An inactive service answers as missing rather than as forbidden. It was
// on the list once, so somebody may hold its URL; saying "this exists but
// you may not have it" tells them something the business has decided not
// to offer, and 404 is both truer to the public's view and the same answer
// PublicSlots gives.
func (h *Handlers) bookableService(r *http.Request, oc orgContext) (sqlc.Service, error) {
	svc, err := sqlc.New(h.pool).GetServiceBySlug(r.Context(), sqlc.GetServiceBySlugParams{
		OrgID: oc.row.ID,
		Slug:  r.PathValue("serviceSlug"),
	})
	if err != nil {
		return sqlc.Service{}, err
	}
	if !svc.Active {
		return sqlc.Service{}, pgx.ErrNoRows
	}
	return svc, nil
}

// parseDate reads ?date=YYYY-MM-DD in the org's zone. An empty value means
// today, which is what a picker opens on.
func parseDate(raw string, oc orgContext) (time.Time, error) {
	if raw == "" {
		return oc.day, nil
	}
	return time.ParseInLocation(serviceDayLayout, raw, oc.loc)
}
