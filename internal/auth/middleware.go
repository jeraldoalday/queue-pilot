package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// RequireAuth resolves the session cookie into a tenant identity in the
// request context. It is the only place in the authenticated app
// (/app/*) that reads the session cookie — every downstream handler and
// service gets OrgID/UserID/Role from context, never from the cookie
// directly.
func RequireAuth(sessions *session.Store, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(session.CookieName)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ident, err := sessions.Get(r.Context(), cookie.Value)
			if err != nil {
				if !errors.Is(err, session.ErrNotFound) {
					log.Error("session lookup failed", "error", err)
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ctx := tenant.WithIdentity(r.Context(), ident.OrgID, ident.UserID, ident.Role)
			ctx = withIdentity(ctx, ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
