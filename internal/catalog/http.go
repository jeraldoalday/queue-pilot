package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
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

func (h *Handlers) GetServices(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	services, staff, assignments, err := h.loadBoard(r.Context(), orgID)
	if err != nil {
		h.log.Error("GetServices: load", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	render(w, r, ServicesPage(services, staff, assignments, "", ident.CSRFToken))
}

func (h *Handlers) PostServices(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	in := ServiceInput{
		Name:            r.FormValue("name"),
		Description:     r.FormValue("description"),
		DurationMinutes: atoiOrZero(r.FormValue("duration_minutes")),
	}

	if _, err := CreateService(r.Context(), h.pool, orgID, in); err != nil {
		h.renderSectionWithError(w, r, orgID, ident.CSRFToken, h.userFacingError(err))
		return
	}

	h.renderSection(w, r, orgID, ident.CSRFToken, "")
}

func (h *Handlers) GetServiceEdit(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	serviceID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	svc, err := GetService(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	render(w, r, ServiceRowEdit(svc, ident.CSRFToken))
}

// GetServiceRow is the "cancel edit" target: re-renders the row in view
// mode without saving anything.
func (h *Handlers) GetServiceRow(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	serviceID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	svc, err := GetService(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	staff, err := ListOrgStaff(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("GetServiceRow: list staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	assigned, err := AssignedStaffIDs(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		h.log.Error("GetServiceRow: assigned staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, ServiceRowView(svc, staff, assigned, ident.CSRFToken))
}

func (h *Handlers) PutService(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())
	serviceID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	in := ServiceInput{
		Name:            r.FormValue("name"),
		Description:     r.FormValue("description"),
		DurationMinutes: atoiOrZero(r.FormValue("duration_minutes")),
		Active:          r.FormValue("active") != "",
	}

	svc, err := UpdateService(r.Context(), h.pool, orgID, serviceID, in)
	if err != nil {
		// On a validation error, redisplay the edit form; the row still
		// carries the previously-saved values, which is an acceptable
		// scaffold-level tradeoff over threading the rejected input back.
		if existing, gerr := GetService(r.Context(), h.pool, orgID, serviceID); gerr == nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			render(w, r, ServiceRowEdit(existing, ident.CSRFToken))
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	staff, err := ListOrgStaff(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("PutService: list staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	assigned, err := AssignedStaffIDs(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		h.log.Error("PutService: assigned staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, ServiceRowView(svc, staff, assigned, ident.CSRFToken))
}

func (h *Handlers) DeleteServiceHandler(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	serviceID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	if err := DeleteService(r.Context(), h.pool, orgID, serviceID); err != nil {
		// Every failure used to come back as the same guess — "it may
		// already be used by appointments" — whether or not it was. An
		// admin whose database was briefly unreachable was told their own
		// data was the problem, was offered nothing to do about it, and
		// nothing was logged for whoever could have seen the real cause.
		h.renderSectionWithError(w, r, orgID, ident.CSRFToken, h.deleteError(err, serviceID))
		return
	}

	h.renderSection(w, r, orgID, ident.CSRFToken, "")
}

func (h *Handlers) PostToggleStaff(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())
	serviceID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}
	staffID, ok := httpx.PathUUID(w, r, "staffID")
	if !ok {
		return
	}

	if err := ToggleStaffAssignment(r.Context(), h.pool, orgID, serviceID, staffID); err != nil {
		h.log.Error("PostToggleStaff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	svc, err := GetService(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	staff, err := ListOrgStaff(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("PostToggleStaff: list staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	assigned, err := AssignedStaffIDs(r.Context(), h.pool, orgID, serviceID)
	if err != nil {
		h.log.Error("PostToggleStaff: assigned staff", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, ServiceRowView(svc, staff, assigned, ident.CSRFToken))
}

// loadBoard fetches everything ServicesSection needs to render: the full
// service list, the org's staff, and which staff are assigned to which
// service.
func (h *Handlers) loadBoard(ctx context.Context, orgID string) ([]sqlc.Service, []sqlc.ListStaffUsersByOrgRow, map[string]map[string]bool, error) {
	services, err := ListServices(ctx, h.pool, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	staff, err := ListOrgStaff(ctx, h.pool, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	assignments, err := AllAssignments(ctx, h.pool, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	return services, staff, assignments, nil
}

func (h *Handlers) renderSection(w http.ResponseWriter, r *http.Request, orgID, csrfToken, errMsg string) {
	services, staff, assignments, err := h.loadBoard(r.Context(), orgID)
	if err != nil {
		h.log.Error("renderSection: load", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, ServicesSection(services, staff, assignments, errMsg, csrfToken))
}

func (h *Handlers) renderSectionWithError(w http.ResponseWriter, r *http.Request, orgID, csrfToken, msg string) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	h.renderSection(w, r, orgID, csrfToken, msg)
}

// deleteError words the one failure a service delete can legitimately
// hit, and treats everything else as what it is: unexpected, logged, and
// not the admin's fault.
func (h *Handlers) deleteError(err error, serviceID string) string {
	if errors.Is(err, ErrServiceInUse) {
		// Naming the way forward matters more than naming the cause. The
		// service cannot be deleted and never will be — turning it off is
		// what the admin actually wanted, and it takes it off the booking
		// page and out of the schedule's picker just the same.
		return "This service has appointments recorded against it, so it can't be deleted. Turn it off instead — it disappears from online booking and from new bookings, and its history stays intact."
	}
	h.log.Error("DeleteServiceHandler", "error", err, "service_id", serviceID)
	return "Couldn't delete that service. Please try again."
}

func (h *Handlers) userFacingError(err error) string {
	var verr ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, ErrSlugTaken):
		return "A service with a conflicting name already exists."
	default:
		h.log.Error("catalog handler error", "error", err)
		return "Something went wrong. Please try again."
	}
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
