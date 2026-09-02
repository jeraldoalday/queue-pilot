package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/config"
	"github.com/jeraldoalday/queuepilot/internal/platform/metrics"
)

func TestRouteLabel(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"the front page", "/", "/"},
		{"an asset", "/static/css/app.css", "/static"},
		{"another asset is the same series", "/static/vendor/htmx.min.js", "/static"},
		{"health", "/healthz", "/healthz"},

		{"a business's lines", "/q/acme-dental", "/q/:org"},
		{"the line list's poll", "/q/acme-dental/lines", "/q/:org/lines"},
		{"one queue's board", "/q/acme-dental/walk-ins", "/q/:org/:queue"},
		{"the board's poll", "/q/acme-dental/walk-ins/board", "/q/:org/:queue/board"},
		{"the waiting-room display", "/q/acme-dental/walk-ins/display", "/q/:org/:queue/display"},
		{"the display's poll", "/q/acme-dental/walk-ins/display/board", "/q/:org/:queue/display/board"},
		{"taking a number", "/q/acme-dental/walk-ins/tickets", "/q/:org/:queue/tickets"},
		{"a customer's ticket", "/q/acme-dental/walk-ins/t/1f97-abc", "/q/:org/:queue/t/:ticket"},
		{"that ticket's poll", "/q/acme-dental/walk-ins/t/1f97-abc/status", "/q/:org/:queue/t/:ticket/status"},

		{"a service list", "/book/acme-dental", "/book/:org"},
		{"one service's picker", "/book/acme-dental/cleaning", "/book/:org/:service"},
		{"a confirmation page", "/book/acme-dental/a/1f97-abc", "/book/:org/a/:appointment"},
		{"its calendar file", "/book/acme-dental/a/1f97-abc/calendar.ics", "/book/:org/a/:appointment/calendar.ics"},

		{"the original board payload", "/api/q/acme-dental/walk-ins", "/api/q/:org/:queue"},
		{"the API's front door", "/api/v1/orgs/acme-dental", "/api/v1/orgs/:org"},
		{"an API board", "/api/v1/orgs/acme-dental/queues/walk-ins", "/api/v1/orgs/:org/queues/:queue"},
		{"an API ticket", "/api/v1/orgs/acme-dental/queues/walk-ins/tickets/abc", "/api/v1/orgs/:org/queues/:queue/tickets/:ticket"},
		{"an API slot lookup", "/api/v1/orgs/acme-dental/services/cleaning/slots", "/api/v1/orgs/:org/services/:service/slots"},
		{"an API calendar file", "/api/v1/orgs/acme-dental/appointments/abc/calendar.ics", "/api/v1/orgs/:org/appointments/:appointment/calendar.ics"},

		{"the dashboard", "/app", "/app"},
		{"a console", "/app/queues/11111111-1111-1111-1111-111111111111/console", "/app/queues/:id/console"},
		{"a report", "/app/queues/11111111-1111-1111-1111-111111111111/report", "/app/queues/:id/report"},
		{"the schedule", "/app/schedule", "/app/schedule"},

		// The cardinality rule: anything unrecognised collapses rather
		// than minting a series per distinct path.
		{"a path nobody serves", "/wp-admin/setup.php", "other"},
		{"a probe under a real prefix", "/q/acme/walk-ins/../../etc", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeLabel(tt.path); got != tt.want {
				t.Errorf("routeLabel(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// The label is a template because a series exists per distinct label
// combination: recording paths would create one per queue per org, driven
// by strangers, growing until the process restarts.
func TestRouteLabelsAreBoundedAcrossManyTenants(t *testing.T) {
	seen := map[string]bool{}
	for _, org := range []string{"acme", "bakery", "clinic", "dentist"} {
		for _, queue := range []string{"walk-ins", "returns", "prescriptions"} {
			seen[routeLabel("/q/"+org+"/"+queue)] = true
			seen[routeLabel("/q/"+org+"/"+queue+"/board")] = true
		}
	}

	if len(seen) != 2 {
		t.Errorf("twelve tenants' boards produced %d labels, want 2: %v", len(seen), seen)
	}
}

func TestMetricsMiddlewareRecordsWhatTheHandlerReturned(t *testing.T) {
	reg := metrics.New()
	handler := Metrics(reg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/q/acme/walk-ins", nil))

	var sb strings.Builder
	reg.Write(&sb, namespace)
	want := `queuepilot_http_requests_total{route="/q/:org/:queue",method="GET",status="404"} 1`
	if !strings.Contains(sb.String(), want) {
		t.Errorf("scrape does not contain %q:\n%s", want, sb.String())
	}
}

// With no token there is no registry, and the middleware should not be
// timing work nobody can read.
func TestMetricsMiddlewareIsAPassThroughWhenDisabled(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	Metrics(nil)(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/app", nil))

	if !called {
		t.Error("the request did not reach the handler")
	}
}

func metricsServer(t *testing.T, token string) *Server {
	t.Helper()
	cfg := config.Config{MetricsToken: token}
	return &Server{
		cfg:       cfg,
		log:       slog.New(slog.DiscardHandler),
		metrics:   newRegistry(cfg),
		startedAt: time.Now().Add(-90 * time.Second),
	}
}

func TestMetricsEndpointNeedsTheToken(t *testing.T) {
	s := metricsServer(t, "s3cret")
	s.metrics.Observe("/app", "GET", 200, 5*time.Millisecond)

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"no header at all", "", http.StatusNotFound},
		{"the wrong token", "Bearer nope", http.StatusNotFound},
		{"the right token under the wrong scheme", "Basic s3cret", http.StatusNotFound},
		{"the right token", "Bearer s3cret", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			s.handleMetrics(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// A refusal is a 404, not a 401: whether this deployment collects metrics
// is not something a stranger needs told.
func TestMetricsEndpointIsInvisibleWhenUnconfigured(t *testing.T) {
	s := metricsServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	s.handleMetrics(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if s.metrics != nil {
		t.Error("a registry was allocated for an endpoint nothing can read")
	}
}

func TestMetricsScrapeCarriesTheBuildAndUptime(t *testing.T) {
	s := metricsServer(t, "s3cret")
	s.metrics.Observe("/book/:org", "GET", 200, 20*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	s.handleMetrics(rec, req)

	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); ct != metrics.ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, metrics.ContentType)
	}
	// "The error rate rose at 14:05" and "we deployed at 14:04" should be
	// one query rather than two systems correlated by hand.
	if !strings.Contains(body, "queuepilot_build_info{revision=") {
		t.Errorf("no build line in the scrape:\n%s", body)
	}
	if !strings.Contains(body, "queuepilot_uptime_seconds ") {
		t.Errorf("no uptime in the scrape:\n%s", body)
	}
	if !strings.Contains(body, `queuepilot_http_requests_total{route="/book/:org",method="GET",status="200"} 1`) {
		t.Errorf("the observation is missing from the scrape:\n%s", body)
	}
}

// The endpoint has to be reachable at the path a scraper is configured
// with, through the whole middleware stack.
func TestMetricsIsRoutedAndCountsItsOwnStack(t *testing.T) {
	cfg := config.Config{MetricsToken: "s3cret"}
	s := newRoutesOnlyServer()
	s.cfg = cfg
	s.metrics = newRegistry(cfg)
	s.startedAt = time.Now()
	routes := s.Routes()

	// A request that goes nowhere: it is counted, and it is counted under
	// a template rather than under the path somebody probed with.
	routes.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/wp-admin/setup.php", nil))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "wp-admin") {
		t.Errorf("a probed path became a label:\n%s", body)
	}
	if !strings.Contains(body, `route="other"`) {
		t.Errorf("the probe was not counted:\n%s", body)
	}
}
