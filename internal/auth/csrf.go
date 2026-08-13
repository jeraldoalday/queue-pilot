package auth

import (
	"bytes"
	"crypto/subtle"
	"io"
	"net/http"
	"net/url"
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

		if err := parseFormAnyMethod(r); err != nil {
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

// parseFormAnyMethod populates r.Form from an application/x-www-form-urlencoded
// body regardless of HTTP method. r.ParseForm only reads the body for
// POST/PUT/PATCH — a plain DELETE (which htmx uses for hx-delete, carrying
// hx-vals like our CSRF token in the body) is left with an empty r.Form,
// which would make every DELETE fail CSRF verification. The body is
// restored afterward so downstream handlers can still read it if needed.
func parseFormAnyMethod(r *http.Request) error {
	if r.Method != http.MethodDelete {
		return r.ParseForm()
	}

	if err := r.ParseForm(); err != nil {
		return err
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return err
	}
	if r.Form == nil {
		r.Form = url.Values{}
	}
	for k, vs := range values {
		r.Form[k] = append(r.Form[k], vs...)
	}
	return nil
}
