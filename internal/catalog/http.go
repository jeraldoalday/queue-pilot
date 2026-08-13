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

	svc, err := GetService(r.Context(), h.pool, orgID, r.PathValue("id"))
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

	svc, err := GetService(r.Context(), h.pool, orgID, r.PathValue("id"))
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
	assigned, err := AssignedStaffIDs(r.Context(), h.pool, orgID, r.PathValue("id"))
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
	serviceID := r.PathValue("id")

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

	if err := DeleteService(r.Context(), h.pool, orgID, r.PathValue("id")); err != nil {
		h.renderSectionWithError(w, r, orgID, ident.CSRFToken,
			"Couldn't delete that service — it may already be used by appointments.")
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
	serviceID := r.PathValue("id")

	if err := ToggleStaffAssignment(r.Context(), h.pool, orgID, serviceID, r.PathValue("staffID")); err != nil {
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
