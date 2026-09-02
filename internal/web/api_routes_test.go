package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/api"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/ratelimit"
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

// Every failure this API produces is an error envelope with a code from a
// closed set — except, until now, the one produced by middleware rather
// than by a handler. A 429 arriving as plain text is the response a front
// end is most likely to meet under load, and it was the one it had to
// recognise from a bare status code.
func TestAPIRateLimitRefusalIsTheErrorEnvelope(t *testing.T) {
	s := apiOnlyServer()
	s.publicLimit = ratelimit.New(1, time.Minute, clock.Real{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/demo/queues/walk-ins/tickets", http.NoBody)
	// Spend the one token this client has, so the request below is
	// refused by the limiter before it can reach the nil pool.
	s.publicLimit.Allow(ratelimit.ClientKey(req, false))

	rec := httptest.NewRecorder()
	s.apiRoutes(s.limitAPI()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	// A well-behaved client backs off by the amount it is told, so the
	// header has to survive the change of refusal. The limiter runs on the
	// real clock here, so the wait is "most of a minute" rather than
	// exactly one — what matters is that it is present and positive.
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 || retry > 60 {
		t.Errorf("Retry-After = %q, want a whole number of seconds up to 60", rec.Header().Get("Retry-After"))
	}

	var envelope struct {
		Error api.Error `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("body was not the API's error envelope: %v", err)
	}
	if envelope.Error.Code != api.CodeRateLimited {
		t.Errorf("code = %q, want %q", envelope.Error.Code, api.CodeRateLimited)
	}
	if envelope.Error.Message == "" {
		t.Error("the envelope carries no message for a person to read")
	}
}

// The API's writes and the public forms' draw on one allowance. They are
// the same two actions in two spellings, and a script alternating between
// them must not get twice the rate the limit claims to grant.
func TestAPIAndFormsShareOneAllowance(t *testing.T) {
	s := apiOnlyServer()
	s.publicLimit = ratelimit.New(1, time.Minute, clock.Real{})

	form := httptest.NewRequest(http.MethodPost, "/q/demo/walk-ins/tickets", http.NoBody)
	formLimited := ratelimit.Middleware(s.publicLimit, false, s.log)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	formLimited.ServeHTTP(rec, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("the form's first request = %d, want 200", rec.Code)
	}

	// Same client, same allowance, other spelling.
	apiReq := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/demo/queues/walk-ins/tickets", http.NoBody)
	apiRec := httptest.NewRecorder()
	s.apiRoutes(s.limitAPI()).ServeHTTP(apiRec, apiReq)

	if apiRec.Code != http.StatusTooManyRequests {
		t.Errorf("the API write after the form's = %d, want 429 — the two do not share a limiter", apiRec.Code)
	}
}
