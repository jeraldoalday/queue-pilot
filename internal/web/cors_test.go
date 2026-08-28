package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func corsHandler() http.Handler {
	return CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("handler ran"))
	}))
}

// A JSON POST from a browser is preceded by an OPTIONS the app has to
// answer. Before this middleware that OPTIONS reached the router, matched
// nothing, and came back 405 — so the POST behind it was never sent and
// the front end saw a CORS error with no failing request to point at.
func TestCORSAnswersThePreflightAJSONBodyRequires(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/orgs/demo/queues/walk-ins/tickets", nil)
	req.Header.Set("Origin", "https://book.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	rec := httptest.NewRecorder()
	corsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != corsAllowedMethods {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, corsAllowedMethods)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != corsAllowedHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, corsAllowedHeaders)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != strconv.Itoa(int(corsMaxAge.Seconds())) {
		t.Errorf("Access-Control-Max-Age = %q, want %v in seconds", got, corsMaxAge)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("preflight reached the handler and got body %q — it should be answered and stopped", body)
	}
}

// The preflight's answer is decided by what was asked, so a shared cache
// keying it on the URL alone would serve one caller's answer to another.
func TestCORSVariesThePreflightOnWhatWasAsked(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/orgs/demo", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")

	rec := httptest.NewRecorder()
	corsHandler().ServeHTTP(rec, req)

	vary := rec.Header().Values("Vary")
	for _, want := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		found := false
		for _, v := range vary {
			if v == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Vary = %v, missing %q", vary, want)
		}
	}
}

// An OPTIONS without the preflight header is not a preflight — it is a
// client asking what a URL supports. Swallowing it here would take that
// answer away from a router that may one day give one.
func TestCORSPassesThroughAPlainOptions(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/orgs/demo", nil)
	rec := httptest.NewRecorder()
	corsHandler().ServeHTTP(rec, req)

	if rec.Body.String() != "handler ran" {
		t.Errorf("body = %q, want the wrapped handler to have run", rec.Body.String())
	}
}

// The ordinary request still has to carry the header; the preflight only
// buys permission to send it.
func TestCORSAllowsTheActualRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/demo", nil)
	req.Header.Set("Origin", "https://example.com")

	rec := httptest.NewRecorder()
	corsHandler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if rec.Body.String() != "handler ran" {
		t.Error("the wrapped handler did not run")
	}
}

// Allowing credentials alongside a wildcard origin is rejected by every
// browser, and would be wrong even if it were not: it is the header that
// turns "anyone may read this public data" into "anyone's page may act as
// the signed-in user".
func TestCORSNeverAllowsCredentials(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/v1/orgs/demo", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "POST")

		rec := httptest.NewRecorder()
		corsHandler().ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Credentials = %q, want it unset", method, got)
		}
	}
}

// The advertised methods are the ones the API serves. A preflight that
// approves DELETE only for the router to refuse it costs a caller a round
// trip to learn something this list already knew.
func TestCORSAdvertisesOnlyTheMethodsTheAPIServes(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
		if !CORSAllows(m) {
			t.Errorf("%s is served by the API but not advertised", m)
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if CORSAllows(m) {
			t.Errorf("%s is advertised but the API does not serve it", m)
		}
	}
}

// A browser caps how long it will trust a preflight; asking for longer is
// silently reduced, which makes an over-long value a lie in the logs.
func TestCORSMaxAgeStaysWithinWhatBrowsersHonour(t *testing.T) {
	if corsMaxAge > 24*time.Hour {
		t.Errorf("corsMaxAge = %v, longer than the 24h browsers will honour", corsMaxAge)
	}
}
