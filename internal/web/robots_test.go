package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/ratelimit"
)

func TestRobotsTagFor(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		indexable bool
	}{
		// The pages a business wants found.
		{"the front page", "/", true},
		{"a business's service list", "/book/acme-dental", true},
		{"one service's picker", "/book/acme-dental/cleaning", true},
		{"a business's list of open lines", "/q/acme-dental", true},
		{"a queue's public board", "/q/acme-dental/walk-ins", true},
		{"the waiting-room display", "/q/acme-dental/walk-ins/display", true},

		// The pages whose URL is the credential.
		{
			// The one that matters most: anyone holding this URL can read
			// the customer's number and cancel it.
			name: "a customer's own ticket",
			path: "/q/acme-dental/walk-ins/t/1f977533-a4e8-42a6-a8d2-c6da792d2c63",
		},
		{"a ticket's polling partial", "/q/acme-dental/walk-ins/t/1f977533/status", false},
		{"a customer's own booking", "/book/acme-dental/a/1f977533-a4e8-42a6-a8d2-c6da792d2c63", false},
		{"that booking's calendar file", "/book/acme-dental/a/1f977533/calendar.ics", false},

		// Everything else, whether or not anyone thought about it.
		{"the staff application", "/app/queues", false},
		{"sign-in", "/login", false},
		{"the JSON API", "/api/v1/orgs/acme-dental", false},
		{"the board's polling partial", "/q/acme-dental/walk-ins/board", false},
		{"the line list's polling partial", "/q/acme-dental/lines", false},
		{"a path nobody has invented yet", "/something/new", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := robotsTagFor(tt.path)
			if tt.indexable && got != "" {
				t.Errorf("robotsTagFor(%q) = %q, want no header — this page should be findable", tt.path, got)
			}
			if !tt.indexable && got != robotsTag {
				t.Errorf("robotsTagFor(%q) = %q, want %q", tt.path, got, robotsTag)
			}
		})
	}
}

// The header, not the file, is what actually keeps a page out of an index:
// a crawler that ignores robots.txt still sees this, and a page already
// indexed is dropped by it.
func TestSecurityHeadersMarkPrivatePagesNoIndex(t *testing.T) {
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	private := httptest.NewRecorder()
	handler.ServeHTTP(private, httptest.NewRequest(http.MethodGet, "/q/acme/walk-ins/t/abc", nil))
	if got := private.Header().Get("X-Robots-Tag"); got != robotsTag {
		t.Errorf("a ticket page's X-Robots-Tag = %q, want %q", got, robotsTag)
	}

	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/book/acme", nil))
	if got := public.Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("a booking page's X-Robots-Tag = %q, want none — a business wants this found", got)
	}
}

func TestRobotsTxt(t *testing.T) {
	s := &Server{log: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	s.handleRobots(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{"User-agent: *", "Disallow: /q/*/t/", "Disallow: /book/*/a/", "Disallow: /api/"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}
	// Which businesses use the product is not something to publish at a
	// well-known address.
	if strings.Contains(strings.ToLower(body), "sitemap") {
		t.Errorf("robots.txt advertises a sitemap:\n%s", body)
	}
}

// newRoutesOnlyServer builds a Server whose routing table can be exercised
// without a database. Every handler is a method value on a nil receiver,
// which is fine as long as the test only asks for paths that answer
// without touching the pool — robots.txt is one of them.
func newRoutesOnlyServer() *Server {
	return &Server{
		log:         slog.New(slog.DiscardHandler),
		publicLimit: ratelimit.New(publicBurst, publicInterval, clock.Real{}),
	}
}

// robots.txt has to be reachable at the address crawlers actually ask for.
func TestRobotsTxtIsRouted(t *testing.T) {
	rec := httptest.NewRecorder()
	newRoutesOnlyServer().Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "User-agent") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
