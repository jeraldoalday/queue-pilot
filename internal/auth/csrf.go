package auth

import (
	"crypto/subtle"
	"net/http"
)

// RequireCSRF must run after RequireAuth. It only checks state-changing
// methods — a session's csrf_token (issued at login/signup, see
// internal/session) must be present as a form field and match, or the
// request is rejected. GET/HEAD/OPTIONS are never mutating, so they pass
// through unchecked.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		ident, ok := CurrentIdentity(r.Context())
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		got := r.FormValue("csrf_token")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(ident.CSRFToken)) != 1 {
			http.Error(w, "invalid or missing csrf token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
