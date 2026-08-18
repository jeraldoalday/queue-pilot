package queue

import (
	"net/http"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/platform/httpx"
)

// GetReport renders one queue's worked day.
//
// It is open to staff rather than admins only, matching the console: the
// people running a counter are the ones who benefit from knowing when
// yesterday's rush was, and the page reveals nothing an admin would want
// withheld from them — it is their own queue's history, without customer
// names.
func (h *Handlers) GetReport(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	queueID, ok := httpx.PathUUID(w, r, "id")
	if !ok {
		return
	}

	loc, err := org.Location(r.Context(), h.pool, orgID)
	if err != nil {
		h.boardError(w, r, err, "GetReport: org location")
		return
	}
	queue, err := Get(r.Context(), h.pool, orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "GetReport: queue")
		return
	}

	day := parseDay(r.URL.Query().Get("date"), loc)
	report, err := LoadReport(r.Context(), h.pool, queue, day, loc)
	if err != nil {
		h.fail(w, "GetReport: load", err)
		return
	}

	render(w, r, ReportPage(report,
		day.AddDate(0, 0, -1).Format(dateLayout),
		day.AddDate(0, 0, 1).Format(dateLayout),
		today(loc).Format(dateLayout),
	))
}

// parseDay reads the ?date= parameter as a service day in the org's own
// timezone.
//
// A missing or unparseable value falls back to today rather than erroring.
// The parameter is a navigation convenience, not an identifier: someone
// who hand-edits it, or a link that loses it, should land on the report
// they most likely wanted instead of on a 400 they have to back out of.
func parseDay(value string, loc *time.Location) time.Time {
	if value == "" {
		return today(loc)
	}
	parsed, err := time.ParseInLocation(dateLayout, value, loc)
	if err != nil {
		return today(loc)
	}
	return parsed
}
