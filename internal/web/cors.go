package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The public JSON API is readable and writable from any origin, and this
// is the middleware that says so in the terms a browser checks.
//
// Two things make that safe, and both are properties of the API rather
// than of this file. The payloads carry nothing a waiting-room display
// would not show a whole room — no names, no phone numbers — so there is
// nothing for a permissive policy to leak on the way out. And no endpoint
// under it reads a session cookie: the tenant comes from the org slug in
// the path, and a customer's own ticket or appointment is identified by an
// unguessable UUID they are holding. Credentials are therefore never
// allowed, which is what keeps "any origin" from meaning "any origin
// acting as the signed-in user".
//
// The existing board endpoint already set Access-Control-Allow-Origin by
// hand, which is enough for a `fetch` that sends no headers of its own. It
// is not enough for the front end: a JSON POST sets Content-Type, and that
// makes the request non-simple, so the browser asks permission first with
// an OPTIONS it was never answering.
const (
	// corsAllowedMethods is every method the API actually offers. It is
	// listed rather than reflected back from the request, so a preflight
	// for something the router does not serve is refused here rather than
	// answered and then 405'd a round trip later.
	corsAllowedMethods = "GET, POST, OPTIONS"

	// corsAllowedHeaders is what a caller may set. Content-Type is what a
	// JSON body needs. Nothing here reads an Authorization header — the
	// API has no bearer tokens — so allowing one would advertise an
	// authentication scheme that does not exist.
	corsAllowedHeaders = "Content-Type"

	// corsMaxAge is how long a browser may skip re-asking. A day is the
	// usual ceiling browsers will honour, and the answer is a constant:
	// there is no per-origin decision here that could go stale sooner.
	corsMaxAge = 24 * time.Hour
)

// CORS answers cross-origin requests for the public JSON API, including
// the preflight that a JSON body makes mandatory.
//
// A preflight is answered here and not passed on. It carries no body and
// asks nothing of the database, and forwarding it would mean every handler
// below growing a branch for a method it does not implement.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			h.Set("Access-Control-Max-Age", strconv.Itoa(int(corsMaxAge.Seconds())))
			// A preflight's answer depends on what was asked, so a shared
			// cache must not serve one origin's preflight from another's.
			// The response body is identical either way; this is about the
			// header set, which is the whole content of the answer.
			h.Add("Vary", "Origin")
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// CORSAllows reports whether the API offers a method, so the router and a
// preflight cannot disagree about what is on offer. It is what the tests
// assert against, and it reads the same constant the header is built from.
func CORSAllows(method string) bool {
	for _, m := range strings.Split(corsAllowedMethods, ",") {
		if strings.EqualFold(strings.TrimSpace(m), method) {
			return true
		}
	}
	return false
}
