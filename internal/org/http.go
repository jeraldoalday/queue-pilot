package org

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type Handlers struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewHandlers(pool *pgxpool.Pool, log *slog.Logger) *Handlers {
	return &Handlers{pool: pool, log: log}
}

func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	o, err := Get(r.Context(), h.pool, orgID)
	if err != nil {
		h.fail(w, "GetSettings", err)
		return
	}
	render(w, r, SettingsPage(o, time.Now(), "", "", ident.CSRFToken))
}

// PostSettings saves the organization's name and timezone.
//
// A rejected save re-renders the form with what was typed still in it and
// a 422, rather than redirecting: someone who mistyped a zone name should
// be able to fix the two characters they got wrong, not retype the whole
// form.
func (h *Handlers) PostSettings(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.orgID(w, r)
	if !ok {
		return
	}
	ident, _ := auth.CurrentIdentity(r.Context())

	in := Settings{
		Name:     r.FormValue("name"),
		Timezone: r.FormValue("timezone"),
	}

	updated, err := Update(r.Context(), h.pool, orgID, in)
	if err != nil {
		// The org row is re-read rather than reusing the rejected input,
		// so the surrounding page (the public address panel, the zone
		// hint) describes what is actually stored. The two fields keep
		// what was typed, because those are what needs correcting.
		current, gerr := Get(r.Context(), h.pool, orgID)
		if gerr != nil {
			h.fail(w, "PostSettings: reread", gerr)
			return
		}
		current.Name = in.Name
		current.Timezone = in.Timezone

		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, SettingsForm(current, time.Now(), h.userFacingError(err), "", ident.CSRFToken))
		return
	}

	render(w, r, SettingsForm(updated, time.Now(), "", "Settings saved.", ident.CSRFToken))
}

func (h *Handlers) orgID(w http.ResponseWriter, r *http.Request) (string, bool) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	return orgID, true
}

func (h *Handlers) fail(w http.ResponseWriter, op string, err error) {
	h.log.Error(op, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handlers) userFacingError(err error) string {
	var verr ValidationError
	if errors.As(err, &verr) {
		return verr.Error()
	}
	h.log.Error("org settings error", "error", err)
	return "Something went wrong. Please try again."
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
