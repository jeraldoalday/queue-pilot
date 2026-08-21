package auth

import (
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

type Handlers struct {
	pool         *pgxpool.Pool
	sessions     *session.Store
	cookieSecure bool
	log          *slog.Logger
}

func NewHandlers(pool *pgxpool.Pool, sessions *session.Store, cookieSecure bool, log *slog.Logger) *Handlers {
	return &Handlers{pool: pool, sessions: sessions, cookieSecure: cookieSecure, log: log}
}

func (h *Handlers) GetSignup(w http.ResponseWriter, r *http.Request) {
	render(w, r, SignupPage(""))
}

func (h *Handlers) PostSignup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	in := SignupInput{
		OrgName:    r.FormValue("org_name"),
		AdminName:  r.FormValue("admin_name"),
		AdminEmail: r.FormValue("email"),
		Password:   r.FormValue("password"),
		UserAgent:  r.UserAgent(),
		IP:         clientIP(r),
	}

	ident, err := Signup(r.Context(), h.pool, h.sessions, in)
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, SignupPage(h.userFacingError(err)))
		return
	}

	session.SetCookie(w, ident.Token, ident.ExpiresAt, h.cookieSecure)
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (h *Handlers) GetLogin(w http.ResponseWriter, r *http.Request) {
	render(w, r, LoginPage(""))
}

func (h *Handlers) PostLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	ident, err := Login(r.Context(), h.pool, h.sessions, r.FormValue("email"), r.FormValue("password"), r.UserAgent(), clientIP(r))
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		render(w, r, LoginPage(h.userFacingError(err)))
		return
	}

	session.SetCookie(w, ident.Token, ident.ExpiresAt, h.cookieSecure)
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (h *Handlers) PostLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(session.CookieName); err == nil {
		if err := Logout(r.Context(), h.sessions, cookie.Value); err != nil {
			h.log.Error("logout", "error", err)
		}
	}
	session.ClearCookie(w, h.cookieSecure)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handlers) GetStaffList(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	ident, _ := CurrentIdentity(r.Context())
	staff, err := ListStaff(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("GetStaffList", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, StaffListPage(staff, ident.CSRFToken))
}

func (h *Handlers) GetNewStaff(w http.ResponseWriter, r *http.Request) {
	ident, _ := CurrentIdentity(r.Context())
	render(w, r, NewStaffPage("", ident.CSRFToken))
}

func (h *Handlers) PostNewStaff(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	ident, _ := CurrentIdentity(r.Context())
	in := CreateStaffInput{
		Name:     r.FormValue("name"),
		Email:    r.FormValue("email"),
		Password: r.FormValue("password"),
	}

	if _, err := CreateStaffUser(r.Context(), h.pool, orgID, in); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		render(w, r, NewStaffPage(h.userFacingError(err), ident.CSRFToken))
		return
	}

	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

// PostStaffDisabled suspends or restores one staff account and re-renders
// the staff list, so the row's state and the button offered on it always
// agree.
//
// The desired state arrives as an explicit "disabled" field rather than
// being toggled from what the server currently holds. A toggle sent twice
// — a double-tapped button, a retried request — lands back where it
// started, and the second click's outcome depends on whether the first
// one's response was ever seen. Naming the target state makes the request
// idempotent: asking twice for "disabled" leaves the account disabled.
func (h *Handlers) PostStaffDisabled(w http.ResponseWriter, r *http.Request) {
	orgID, err := tenant.RequireOrgID(r.Context())
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	staffID := r.PathValue("staffID")
	disabled := r.FormValue("disabled") == "true"

	if _, err := SetStaffDisabled(r.Context(), h.pool, orgID, staffID, disabled); err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			// Includes an admin's own ID: the query only matches staff
			// rows, so an admin cannot suspend themselves or a colleague
			// by hand-editing the form. That reads as "no such staff
			// account" rather than as a special-cased refusal.
			http.NotFound(w, r)
			return
		}
		h.log.Error("PostStaffDisabled", "error", err, "staff_user_id", staffID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderStaffList(w, r, orgID)
}

func (h *Handlers) renderStaffList(w http.ResponseWriter, r *http.Request, orgID string) {
	ident, _ := CurrentIdentity(r.Context())

	staff, err := ListStaff(r.Context(), h.pool, orgID)
	if err != nil {
		h.log.Error("renderStaffList", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	render(w, r, StaffTable(staff, ident.CSRFToken))
}

// userFacingError returns a message safe to show the user, logging
// anything else (unexpected DB/internal failures) server-side instead of
// exposing it.
func (h *Handlers) userFacingError(err error) string {
	var verr ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, ErrSlugTaken):
		return "That organization name is already taken — try a slightly different name."
	case errors.Is(err, ErrEmailTaken):
		return "An account with that email already exists."
	case errors.Is(err, ErrInvalidCredentials):
		return "Incorrect email or password."
	default:
		h.log.Error("auth handler error", "error", err)
		return "Something went wrong. Please try again."
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// render sets the HTML content type and writes a templ component,
// centralizing the render-error logging every handler would otherwise
// repeat.
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Default().Error("render", "error", err)
	}
}
