package auth

import (
	"net/http"

	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// RequireRole must run after RequireAuth (it reads the role RequireAuth
// set in context). A mismatch is a 403, not a redirect to login — the
// user is authenticated, just not authorized for this route.
func RequireRole(role tenant.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, err := tenant.RequireRole(r.Context())
			if err != nil || got != role {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
