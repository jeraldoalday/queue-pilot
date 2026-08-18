package availability

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/db"
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

// staffUser fetches the target staff member scoped to orgID — if
// staffID belongs to another org, this returns pgx.ErrNoRows, which the
// caller turns into a 404. This is the same tenant-isolation shape as
// every other org-scoped lookup in the app.
func (h *Handlers) staffUser(r *http.Request, orgID, staffID string) (sqlc.User, error) {
	return sqlc.New(h.pool).GetUserByID(r.Context(), sqlc.GetUserByIDParams{
		ID:    db.MustUUID(staffID),
		OrgID: db.MustUUID(orgID),
	})
}

func (h *Handlers) GetAvailability(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())
	staffID, ok := httpx.PathUUID(w, r, "staffID")
	if !ok {
		return
	}

	staffUser, err := h.staffUser(r, orgID, staffID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hours, err := ListHours(r.Context(), h.pool, orgID, staffID)
	if err != nil {
		h.log.Error("GetAvailability: list hours", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, AvailabilityPage(staffUser, hours, ident.CSRFToken))
}

func (h *Handlers) PostAvailability(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())
	staffID, ok := httpx.PathUUID(w, r, "staffID")
	if !ok {
		return
	}

	staffUser, err := h.staffUser(r, orgID, staffID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	weekday, _ := strconv.Atoi(r.FormValue("weekday"))
	startMin, startErr := parseHHMM(r.FormValue("start_time"))
	endMin, endErr := parseHHMM(r.FormValue("end_time"))

	var addErr error
	if startErr != nil || endErr != nil {
		addErr = ValidationError{"start and end time must be valid times"}
	} else {
		_, addErr = AddHours(r.Context(), h.pool, orgID, staffID, HoursInput{
			Weekday:  weekday,
			StartMin: startMin,
			EndMin:   endMin,
		})
	}

	hours, err := ListHours(r.Context(), h.pool, orgID, staffID)
	if err != nil {
		h.log.Error("PostAvailability: list hours", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if addErr != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	render(w, r, AvailabilityList(staffUser, hours, ident.CSRFToken))
}

func (h *Handlers) DeleteAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())
	staffID, ok := httpx.PathUUID(w, r, "staffID")
	if !ok {
		return
	}

	staffUser, err := h.staffUser(r, orgID, staffID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	hoursID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	if err := DeleteHours(r.Context(), h.pool, orgID, staffID, hoursID); err != nil {
		h.log.Error("DeleteAvailabilityHandler", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hours, err := ListHours(r.Context(), h.pool, orgID, staffID)
	if err != nil {
		h.log.Error("DeleteAvailabilityHandler: list hours", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, AvailabilityList(staffUser, hours, ident.CSRFToken))
}

// parseHHMM parses an HTML <input type="time"> value ("15:04" format).
func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
