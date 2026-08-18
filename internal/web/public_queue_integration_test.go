//go:build integration

package web

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/config"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/platform/logger"
	"github.com/jeraldoalday/queuepilot/internal/queue"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// tenant is one seeded org with an open walk-in queue, plus the running
// test server its public URLs are served from.
//
// These tests drive the real router rather than calling handlers directly,
// because what they are checking — that a public URL can only ever reach
// its own org's data — is a property of the routing and middleware
// together, not of any one function.
type testTenant struct {
	server    *httptest.Server
	pool      *pgxpool.Pool
	orgSlug   string
	queueSlug string
	orgID     string
	queueID   string
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; run via `make test-integration`")
	}

	pool, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	cfg := config.Config{Env: "development", Port: "0", DatabaseURL: dbURL}
	srv := httptest.NewServer(NewServer(cfg, logger.New("development"), pool).Routes())
	t.Cleanup(srv.Close)
	return srv
}

// newTenant seeds an org with one open queue and returns it bound to srv.
// Every tenant in a test shares one server, which is what makes the
// cross-tenant checks meaningful: the isolation has to come from the code,
// not from the two orgs happening to be served by different processes.
func newTenant(t *testing.T, srv *httptest.Server, name string) testTenant {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	pool, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	sessions := session.NewStore(pool, clock.Real{})
	ident, err := auth.Signup(ctx, pool, sessions, auth.SignupInput{
		OrgName:    name + " " + id.New(),
		AdminName:  "Test Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	q, err := queue.Create(ctx, pool, ident.OrgID, queue.Input{Name: "Walk-ins"})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}

	orgRow, err := sqlc.New(pool).GetOrganizationByID(ctx, db.MustUUID(ident.OrgID))
	if err != nil {
		t.Fatalf("read org: %v", err)
	}

	return testTenant{
		server:    srv,
		pool:      pool,
		orgSlug:   orgRow.Slug,
		queueSlug: q.Slug,
		orgID:     ident.OrgID,
		queueID:   db.UUIDString(q.ID),
	}
}

func (tt testTenant) boardPath() string {
	return "/q/" + tt.orgSlug + "/" + tt.queueSlug
}

// get fetches a path and returns the status and body, following redirects
// the way a browser would.
func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()

	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

// join posts the public form without following the redirect, so the test
// can read the Location header the handler sets.
func join(t *testing.T, srv *httptest.Server, path, name string) (int, string) {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+path+"/tickets", url.Values{"customer_name": {name}})
	if err != nil {
		t.Fatalf("POST %s/tickets: %v", path, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, resp.Header.Get("Location")
}

func TestIntegrationPublicJoinAndTrackTicket(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Public Flow Org")

	status, body := get(t, srv, tt.boardPath())
	if status != http.StatusOK {
		t.Fatalf("board = %d, want 200", status)
	}
	if !strings.Contains(body, "Now serving") {
		t.Fatal("the board did not render its now-serving panel")
	}

	status, location := join(t, srv, tt.boardPath(), "Ada Lovelace")
	if status != http.StatusSeeOther {
		t.Fatalf("join = %d, want 303 — joining must redirect so a refresh doesn't take a second number", status)
	}
	if !strings.HasPrefix(location, tt.boardPath()+"/t/") {
		t.Fatalf("join redirected to %q, want the customer's own ticket page", location)
	}

	status, body = get(t, srv, location)
	if status != http.StatusOK {
		t.Fatalf("ticket page = %d, want 200", status)
	}
	for _, want := range []string{"#1", "Ada Lovelace", "Your place in line"} {
		if !strings.Contains(body, want) {
			t.Errorf("the ticket page is missing %q", want)
		}
	}

	// The poll endpoint the page refreshes itself from must serve the same
	// ticket, or a customer watching their phone would see it stop updating.
	status, body = get(t, srv, location+"/status")
	if status != http.StatusOK {
		t.Fatalf("ticket poll = %d, want 200", status)
	}
	if !strings.Contains(body, "#1") {
		t.Fatal("the ticket poll did not return the ticket")
	}
}

// The central guarantee of the public URL space: org B's URL must not
// reach org A's ticket, even with a valid ticket ID in hand.
func TestIntegrationPublicTicketIsScopedToItsQueue(t *testing.T) {
	srv := newTestServer(t)
	orgA := newTenant(t, srv, "Org A")
	orgB := newTenant(t, srv, "Org B")

	_, location := join(t, srv, orgA.boardPath(), "Ada")
	ticketID := location[strings.LastIndex(location, "/")+1:]

	// The same ticket ID, asked for through org B's queue.
	status, _ := get(t, srv, orgB.boardPath()+"/t/"+ticketID)
	if status != http.StatusNotFound {
		t.Fatalf("org B reading org A's ticket = %d, want 404", status)
	}

	// And it is still readable where it belongs, so the 404 above is
	// isolation rather than the ticket simply being broken.
	if status, _ := get(t, srv, location); status != http.StatusOK {
		t.Fatalf("the ticket is unreadable in its own queue = %d, want 200", status)
	}
}

func TestIntegrationPublicNotFoundPathsAreIndistinguishable(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Probe Org")

	paths := map[string]string{
		"an unknown org":     "/q/no-such-org-" + id.New()[:8] + "/walk-ins",
		"an unknown queue":   tt.boardPath() + "-no-such-queue",
		"an unknown ticket":  tt.boardPath() + "/t/00000000-0000-0000-0000-000000000000",
		"a malformed ticket": tt.boardPath() + "/t/notauuid",
		"an unknown display": "/q/no-such-org-" + id.New()[:8] + "/walk-ins/display",
	}

	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			status, body := get(t, srv, path)
			if status != http.StatusNotFound {
				t.Fatalf("%s = %d, want 404", path, status)
			}
			// The body must not name what was missing: a public URL space
			// that distinguishes "no such org" from "no such queue in this
			// org" is a directory of who uses the product.
			if strings.Contains(strings.ToLower(body), "org") && !strings.Contains(body, "404") {
				t.Errorf("the 404 body leaks detail: %q", body)
			}
		})
	}
}

func TestIntegrationClosedQueueRefusesPublicJoin(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Closing Org")

	if _, err := queue.Update(context.Background(), tt.pool, tt.orgID, tt.queueID,
		queue.Input{Name: "Walk-ins", Active: false}); err != nil {
		t.Fatalf("close queue: %v", err)
	}

	// The board still renders — a closed line is a real page with an
	// explanation, not a missing one.
	status, body := get(t, srv, tt.boardPath())
	if status != http.StatusOK {
		t.Fatalf("closed board = %d, want 200", status)
	}
	if !strings.Contains(body, "isn't taking new walk-ins") {
		t.Error("the closed board did not explain why joining is unavailable")
	}
	if strings.Contains(body, "Get a ticket") {
		t.Error("the closed board still offered the join button")
	}

	status, _ = join(t, srv, tt.boardPath(), "Ada")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("joining a closed queue = %d, want 422", status)
	}
}

// The unauthenticated join form is rate limited; the read paths beside it
// are not, since those are the feature's own polling.
func TestIntegrationPublicJoinIsRateLimited(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Flooded Org")

	var limited bool
	for i := 0; i < publicBurst+2; i++ {
		status, _ := join(t, srv, tt.boardPath(), "Flood")
		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("no request was limited within %d attempts", publicBurst+2)
	}

	if status, _ := get(t, srv, tt.boardPath()); status != http.StatusOK {
		t.Fatalf("reading the board while limited = %d, want 200 — polling must keep working", status)
	}
}

// Every authenticated route must refuse an anonymous caller. Checked here
// rather than per package so a route added to the mux without its
// middleware is caught by something.
func TestIntegrationAppRoutesRequireAuth(t *testing.T) {
	srv := newTestServer(t)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       10 * time.Second,
	}

	paths := []string{
		"/app",
		"/app/queues",
		"/app/queues/00000000-0000-0000-0000-000000000000/console",
		"/app/queues/00000000-0000-0000-0000-000000000000/board",
		"/app/schedule",
		"/app/services",
		"/app/staff",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("anonymous GET %s = %d, want a 303 redirect to login", path, resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != "/login" {
				t.Fatalf("anonymous GET %s redirected to %q, want /login", path, got)
			}
		})
	}
}

// post sends an empty form without following the redirect, so a test can
// read the Location the handler sets.
func post(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+path, url.Values{})
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, resp.Header.Get("Location")
}

// A customer who changes their mind leaves the line from their own page,
// and everyone behind them moves up. The whole path is exercised through
// the router because the scoping that makes it safe lives in the URL.
func TestIntegrationCustomerCanLeaveTheLine(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Leave Line Org")

	_, first := join(t, srv, tt.boardPath(), "Ada Lovelace")
	_, second := join(t, srv, tt.boardPath(), "Grace Hopper")

	// The second customer is behind the first: position 2.
	if _, body := get(t, srv, second); !strings.Contains(body, "Your place in line") {
		t.Fatal("the second ticket page did not show a place in line")
	}

	status, location := post(t, srv, first+"/cancel")
	if status != http.StatusSeeOther {
		t.Fatalf("cancel = %d, want 303 — it must redirect so a refresh doesn't re-post", status)
	}
	if location != first {
		t.Fatalf("cancel redirected to %q, want the ticket page %q", location, first)
	}

	status, body := get(t, srv, first)
	if status != http.StatusOK {
		t.Fatalf("cancelled ticket page = %d, want 200", status)
	}
	if !strings.Contains(body, "cancelled") {
		t.Error("the cancelled ticket page does not say the ticket was cancelled")
	}
	if strings.Contains(body, "Leave the line") {
		t.Error("the cancelled ticket page still offers to leave the line")
	}

	// The point of the feature: the customer behind them has moved up, so
	// the queue is no longer counting someone who has gone home. The body
	// is unescaped first — templ escapes the apostrophe in "You're", and a
	// test that matched the escaped form would be asserting on HTML
	// encoding rather than on what the customer reads.
	_, secondBody := get(t, srv, second)
	if !strings.Contains(html.UnescapeString(secondBody), "You're next") {
		t.Error("the customer behind did not move up after the cancellation")
	}
}

// The cancel endpoint is a write on a public URL, so it must obey the
// same tenant scoping every other public read does.
func TestIntegrationCancelIsScopedToItsQueue(t *testing.T) {
	srv := newTestServer(t)
	orgA := newTenant(t, srv, "Cancel Org A")
	orgB := newTenant(t, srv, "Cancel Org B")

	_, location := join(t, srv, orgA.boardPath(), "Ada")
	ticketID := location[strings.LastIndex(location, "/")+1:]

	status, _ := post(t, srv, orgB.boardPath()+"/t/"+ticketID+"/cancel")
	if status != http.StatusNotFound {
		t.Fatalf("org B cancelling org A's ticket = %d, want 404", status)
	}

	// And org A's customer is still in line, so the 404 above stopped the
	// write rather than merely hiding its result.
	_, body := get(t, srv, location)
	if !strings.Contains(body, "Your place in line") {
		t.Fatal("org A's ticket was affected by org B's cancel attempt")
	}
}

// Cancelling twice — a double tap, or a back button followed by another
// submit — must land the customer back on their ticket rather than on an
// error page. The state they end up in is the same either way.
func TestIntegrationCancelIsIdempotentForTheCustomer(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "Double Cancel Org")

	_, location := join(t, srv, tt.boardPath(), "Ada")

	for i := 0; i < 2; i++ {
		if status, _ := post(t, srv, location+"/cancel"); status != http.StatusSeeOther {
			t.Fatalf("cancel attempt %d = %d, want 303", i+1, status)
		}
	}

	_, body := get(t, srv, location)
	if !strings.Contains(body, "cancelled") {
		t.Error("the ticket is not cancelled after two cancel requests")
	}
}
