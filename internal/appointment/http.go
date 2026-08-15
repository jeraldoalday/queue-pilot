package appointment

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type Handlers struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewHandlers(pool *pgxpool.Pool, log *slog.Logger) *Handlers {
	return &Handlers{pool: pool, log: log}
}

func (h *Handlers) GetSchedule(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	loc, err := org.Location(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("GetSchedule: org location", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	day := parseDay(r.URL.Query().Get("date"), loc)

	appts, err := ListForOrgOnDay(r.Context(), h.pool, orgID, day)
	if err != nil {
		h.log.Error("GetSchedule: list appointments", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	services, staff, err := h.servicesAndStaff(r.Context(), orgID)
	if err != nil {
		h.log.Error("GetSchedule: load services/staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	render(w, r, SchedulePage(day, appts, services, staff, loc, ident.CSRFToken))
}

// PostSlots re-renders the booking form whenever the admin changes the
// service or staff selection, so the slot list stays in sync with the
// current choice.
func (h *Handlers) PostSlots(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	loc, err := org.Location(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("PostSlots: org location", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	day := parseDay(r.FormValue("date"), loc)
	serviceID := r.FormValue("service_id")
	staffID := r.FormValue("staff_user_id")

	services, staff, err := h.servicesAndStaff(r.Context(), orgID)
	if err != nil {
		h.log.Error("PostSlots: load services/staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var slots []availability.Slot
	if serviceID != "" && staffID != "" {
		slots, err = AvailableSlots(r.Context(), h.pool, orgID, staffID, serviceID, day)
		if err != nil {
			h.log.Error("PostSlots: available slots", "error", err)
			render(w, r, BookingForm(day, services, staff, nil, serviceID, staffID,
				"Couldn't load available times. Please try again.", loc, ident.CSRFToken))
			return
		}
	}

	render(w, r, BookingForm(day, services, staff, slots, serviceID, staffID, "", loc, ident.CSRFToken))
}

func (h *Handlers) PostAppointment(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	loc, err := org.Location(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("PostAppointment: org location", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	day := parseDay(r.FormValue("date"), loc)
	serviceID := r.FormValue("service_id")
	staffID := r.FormValue("staff_user_id")

	startsAt, parseErr := time.Parse(time.RFC3339, r.FormValue("starts_at"))

	services, staff, err := h.servicesAndStaff(r.Context(), orgID)
	if err != nil {
		h.log.Error("PostAppointment: load services/staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var appt sqlc.Appointment
	var bookErr error
	if parseErr != nil {
		bookErr = ValidationError{"please choose a time slot"}
	} else {
		appt, bookErr = Book(r.Context(), h.pool, orgID, BookInput{
			ServiceID:     serviceID,
			StaffUserID:   staffID,
			CustomerName:  r.FormValue("customer_name"),
			CustomerEmail: r.FormValue("customer_email"),
			CustomerPhone: r.FormValue("customer_phone"),
			StartsAt:      startsAt,
		})
	}

	if bookErr != nil {
		// Re-render with a fresh slot list: if the failure was a lost race
		// for the slot, the stale list would otherwise still offer it.
		slots, slotErr := h.slotsOrNil(r.Context(), orgID, staffID, serviceID, day)
		if slotErr != nil {
			h.log.Error("PostAppointment: reload slots", "error", slotErr)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, BookingForm(day, services, staff, slots, serviceID, staffID,
			h.userFacingError(bookErr), loc, ident.CSRFToken))
		return
	}

	appts, err := ListForOrgOnDay(r.Context(), h.pool, orgID, day)
	if err != nil {
		h.log.Error("PostAppointment: list appointments", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Swap the confirmation into the form slot, and update the day's list
	// out-of-band in the same response.
	render(w, r, BookedConfirmation(appt, day, services, staff, loc, ident.CSRFToken))
	render(w, r, ScheduleListOOB(appts, loc, ident.CSRFToken))
}

func (h *Handlers) slotsOrNil(ctx context.Context, orgID, staffID, serviceID string, day time.Time) ([]availability.Slot, error) {
	if serviceID == "" || staffID == "" {
		return nil, nil
	}
	return AvailableSlots(ctx, h.pool, orgID, staffID, serviceID, day)
}

func (h *Handlers) servicesAndStaff(ctx context.Context, orgID string) ([]sqlc.Service, []sqlc.ListStaffUsersByOrgRow, error) {
	services, err := catalog.ListServices(ctx, h.pool, orgID)
	if err != nil {
		return nil, nil, err
	}
	staff, err := catalog.ListOrgStaff(ctx, h.pool, orgID)
	if err != nil {
		return nil, nil, err
	}
	return services, staff, nil
}

func (h *Handlers) userFacingError(err error) string {
	var verr ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, ErrSlotTaken):
		return "That time was just booked by someone else — please pick another slot."
	case errors.Is(err, ErrStaffNotAssigned):
		return "That staff member doesn't perform the selected service."
	default:
		h.log.Error("appointment handler error", "error", err)
		return "Something went wrong. Please try again."
	}
}

// parseDay reads a yyyy-mm-dd value as a date in the org's timezone,
// falling back to today (also in the org's timezone) when absent or
// malformed. Interpreting the date in loc is what makes a generated
// "09:00" slot mean 09:00 for that business.
func parseDay(s string, loc *time.Location) time.Time {
	if t, err := time.ParseInLocation(dateFormat, s, loc); err == nil {
		return t
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
