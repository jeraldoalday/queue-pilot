package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/api"
)

// apiOnlyServer builds just enough Server to exercise the JSON API's
// routing table. The pool is nil, which is safe for exactly the assertions
// below: they check which handler a path reaches, and the paths tested
// here resolve before any query is made.
func apiOnlyServer() *Server {
	return &Server{
		log: slog.New(slog.DiscardHandler),
		api: api.NewHandlers(nil, slog.New(slog.DiscardHandler)),
	}
}

func passthrough(next http.Handler) http.Handler { return next }

// A front end that gets HTML where it expected an error envelope reports
// whatever its JSON parser said, which is never the actual problem. The
// API's 404 has to be the API's.
func TestUnknownAPIPathAnswersWithAJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	apiOnlyServer().apiRoutes(passthrough).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/orgs/demo/nothing-here", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	var envelope struct {
		Error api.Error `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("body was not the API's error envelope: %v", err)
	}
	if envelope.Error.Code != api.CodeNotFound {
		t.Errorf("code = %q, want %q", envelope.Error.Code, api.CodeNotFound)
	}
}

// The two API writes are the same two actions the public forms offer, and
// a limit one spelling of a request obeys while the other does not is not
// a limit. This asserts the wiring rather than the limiter: the middleware
// records that it wrapped the route.
func TestAPIWritesGoThroughThePublicRateLimit(t *testing.T) {
	var limited []string
	recordLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limited = append(limited, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		})
	}

	routes := apiOnlyServer().apiRoutes(recordLimit)
	writes := []string{
		"/api/v1/orgs/demo/queues/walk-ins/tickets",
		"/api/v1/orgs/demo/queues/walk-ins/tickets/11111111-1111-1111-1111-111111111111/cancel",
		"/api/v1/orgs/demo/services/consult/bookings",
		"/api/v1/orgs/demo/appointments/11111111-1111-1111-1111-111111111111/cancel",
	}
	for _, path := range writes {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, http.NoBody))
		if rec.Code != http.StatusTeapot {
			t.Errorf("POST %s did not pass through the rate limiter (status %d)", path, rec.Code)
		}
	}
	if len(limited) != len(writes) {
		t.Errorf("rate limiter saw %v, want all %d writes", limited, len(writes))
	}
}

// Reads are deliberately unlimited: a board and a ticket page poll by
// design, and throttling them would throttle the feature's own refresh.
func TestAPIReadsAreNotRateLimited(t *testing.T) {
	limiterRan := false
	recordLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limiterRan = true
			next.ServeHTTP(w, r)
		})
	}

	routes := apiOnlyServer().apiRoutes(recordLimit)
	// A read reaches a handler that would query the nil pool, so this only
	// asserts the middleware chain, not the response. The panic the nil
	// pool would raise is caught by the recover the router wraps around
	// the mux in production; here the request is stopped before it.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/demo/queues/walk-ins", nil)
	func() {
		defer func() { _ = recover() }()
		routes.ServeHTTP(httptest.NewRecorder(), req)
	}()

	if limiterRan {
		t.Error("a board read went through the rate limiter — polling pages would throttle themselves")
	}
}

// The preflight has to be answered for every API path, not only the ones
// the inner mux happens to know, or a typo in a front end's URL surfaces
// as a CORS failure instead of a 404.
func TestAPIPreflightIsAnsweredForUnknownPathsToo(t *testing.T) {
	handler := CORS(apiOnlyServer().apiRoutes(passthrough))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/orgs/demo/whatever", nil)
	req.Header.Set("Origin", "https://book.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		body, _ := io.ReadAll(rec.Body)
		t.Errorf("status = %d (%s), want 204", rec.Code, body)
	}
}

// The calendar file is the one response under /api/v1 that is not JSON,
// so it is the one most likely to be dropped by a change to the API's
// routing table. This asserts it is routed at all — the file's contents
// are the appointment package's own tests.
func TestAPICalendarRouteIsReachableAndUnlimited(t *testing.T) {
	limiterRan := false
	recordLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limiterRan = true
			next.ServeHTTP(w, r)
		})
	}

	routes := apiOnlyServer().apiRoutes(recordLimit)
	path := "/api/v1/orgs/demo/appointments/11111111-1111-1111-1111-111111111111/calendar.ics"

	// The handler queries the nil pool, so the request is stopped by the
	// panic that raises rather than by a missing route. What matters here
	// is that it got past the mux: an unrouted path answers the API's JSON
	// 404 without panicking at all.
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}()

	if !panicked {
		t.Error("the calendar path did not reach a handler — it fell through to the API's 404")
	}
	// It is a read, and reads are unlimited here for the same reason the
	// board's are: a download is not what costs a business anything.
	if limiterRan {
		t.Error("a calendar download went through the rate limiter")
	}
}
