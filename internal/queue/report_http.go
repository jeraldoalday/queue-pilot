package queue

import (
	"net/http"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/org"
	"github.com/jeraldoalday/queuepilot/internal/platform/httpx"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
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

	// The download link is only offered to whoever the export route would
	// actually let through, so staff are not shown a button that answers
	// them with a 403.
	ident, _ := auth.CurrentIdentity(r.Context())
	render(w, r, ReportPage(report,
		day.AddDate(0, 0, -1).Format(dateLayout),
		day.AddDate(0, 0, 1).Format(dateLayout),
		today(loc).Format(dateLayout),
		ident.Role == tenant.RoleAdmin,
	))
}

// GetExport downloads one service day's tickets as CSV.
//
// Unlike the report, this is admin-only. The rows carry customer names
// and phone numbers, and a file is a different kind of exposure from a
// screen: it leaves the app, gets emailed, and outlives whatever reason
// it was pulled for. Staff can already read the same names on the console
// while they work the counter; nobody needs a copy of them to do that.
func (h *Handlers) GetExport(w http.ResponseWriter, r *http.Request) {
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
		h.boardError(w, r, err, "GetExport: org location")
		return
	}
	queue, err := Get(r.Context(), h.pool, orgID, queueID)
	if err != nil {
		h.boardError(w, r, err, "GetExport: queue")
		return
	}

	day := parseDay(r.URL.Query().Get("date"), loc)
	tickets, err := ListForDay(r.Context(), h.pool, queueID, day)
	if err != nil {
		h.fail(w, "GetExport: list", err)
		return
	}

	// Headers before the first byte of the body: once WriteCSV starts
	// writing, the status is sent and a Content-Disposition set afterwards
	// would be ignored, leaving the browser to render the file as a page.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", contentDisposition(ExportFilename(queue.Slug, day)))
	// The rows are personal data, and a cached copy of them sitting in a
	// shared browser is not something an admin asked for.
	w.Header().Set("Cache-Control", "no-store")

	if err := WriteCSV(w, tickets, loc); err != nil {
		// The response has already begun, so this cannot become a 500.
		// Logging it is the honest outcome: the client has a truncated
		// file either way, and a silent partial export is worse than a
		// recorded one.
		h.log.Error("GetExport: write", "error", err, "queue_id", queueID)
	}
}

// contentDisposition builds the header that makes a browser save the file
// rather than render it.
//
// The filename is built from a slug and an ISO date, so it cannot contain
// a quote or a newline that would let it escape the header — but it is
// quoted anyway, because a header assembled by concatenation is one
// rename away from being injectable, and the quoting costs nothing.
func contentDisposition(filename string) string {
	return `attachment; filename="` + filename + `"`
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
