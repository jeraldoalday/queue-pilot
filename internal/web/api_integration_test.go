//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/api"
	"github.com/jeraldoalday/queuepilot/internal/queue"
)

// The JSON API against a real database, through the real router.
//
// The view constructors are unit-tested without a database, and the
// routing table is tested without one too. What neither can reach is the
// thing this file is for: that a customer's whole walk-in journey works
// end to end over JSON, and — the reason the public URL space is shaped
// the way it is — that one business's slug cannot reach another's queue.
//
// It drives the router rather than the handlers, because tenant scoping is
// a property of the routing and the handlers together.

// apiGet fetches a JSON path and decodes it into v.
func apiGet(t *testing.T, srv *httptest.Server, path string, v any) int {
	t.Helper()

	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET %s served %q, want JSON", path, ct)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, body)
		}
	}
	return resp.StatusCode
}

// apiPost sends a JSON body and decodes the reply into v.
func apiPost(t *testing.T, srv *httptest.Server, path string, in any, v any) int {
	t.Helper()

	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if v != nil {
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, raw)
		}
	}
	return resp.StatusCode
}

func (tt testTenant) apiOrgPath() string   { return "/api/v1/orgs/" + tt.orgSlug }
func (tt testTenant) apiQueuePath() string { return tt.apiOrgPath() + "/queues/" + tt.queueSlug }

// The whole walk-in journey over JSON: read the front door, read the
// board, take a number, watch the position, leave the line.
func TestIntegrationAPIWalkInJourney(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Journey Org")

	var summary api.OrgSummary
	if status := apiGet(t, srv, tt.apiOrgPath(), &summary); status != http.StatusOK {
		t.Fatalf("org summary = %d, want 200", status)
	}
	if len(summary.Queues) != 1 || summary.Queues[0].Slug != tt.queueSlug {
		t.Fatalf("org summary listed %+v, want the org's one open queue", summary.Queues)
	}
	if summary.Org.Slug != tt.orgSlug {
		t.Errorf("org slug = %q, want %q", summary.Org.Slug, tt.orgSlug)
	}
	// An org with no services still answers with a list, not a null: a
	// front end mapping over the result should not have to guard a case
	// the API never means.
	if summary.Services == nil {
		t.Error("services encoded as null, want an empty array")
	}

	var board api.BoardResponse
	if status := apiGet(t, srv, tt.apiQueuePath(), &board); status != http.StatusOK {
		t.Fatalf("board = %d, want 200", status)
	}
	if board.Queue.NowServing != nil {
		t.Errorf("now_serving = %d on an untouched queue, want null", *board.Queue.NowServing)
	}
	if len(board.WaitingNumbers) != 0 {
		t.Errorf("waiting_numbers = %v on an untouched queue, want empty", board.WaitingNumbers)
	}

	var first api.Ticket
	status := apiPost(t, srv, tt.apiQueuePath()+"/tickets",
		api.JoinRequest{CustomerName: "Ada Lovelace"}, &first)
	if status != http.StatusCreated {
		t.Fatalf("join = %d, want 201", status)
	}
	if first.Number != 1 {
		t.Errorf("first ticket of the day = %d, want 1", first.Number)
	}
	// The board is re-read after the write, so the customer's position
	// counts the customer. Rendering against the pre-insert board would
	// say "you're next" to somebody who is not.
	if first.Position == nil || *first.Position != 1 {
		t.Errorf("position = %v, want 1", first.Position)
	}
	if !first.Cancellable {
		t.Error("a waiting ticket should be cancellable by its holder")
	}

	var second api.Ticket
	apiPost(t, srv, tt.apiQueuePath()+"/tickets",
		api.JoinRequest{CustomerName: "Grace Hopper"}, &second)
	if second.Position == nil || *second.Position != 2 {
		t.Errorf("second customer's position = %v, want 2", second.Position)
	}

	// Reading a ticket back must agree with what issuing it reported.
	var reread api.Ticket
	if status := apiGet(t, srv, tt.apiQueuePath()+"/tickets/"+first.ID, &reread); status != http.StatusOK {
		t.Fatalf("ticket read = %d, want 200", status)
	}
	if reread.Number != first.Number || reread.Status != "waiting" {
		t.Errorf("ticket read back as %+v, want the ticket that was issued", reread)
	}

	// The board now carries the numbers a display draws, and still no
	// names — the payload is served to any origin without a session.
	apiGet(t, srv, tt.apiQueuePath(), &board)
	if len(board.WaitingNumbers) != 2 {
		t.Errorf("waiting_numbers = %v, want two", board.WaitingNumbers)
	}
	encoded, _ := json.Marshal(board)
	for _, name := range []string{"Ada Lovelace", "Grace Hopper"} {
		if strings.Contains(string(encoded), name) {
			t.Errorf("the board payload leaked %q:\n%s", name, encoded)
		}
	}

	// Leaving the line frees the place, and everybody behind moves up.
	var cancelled api.Ticket
	if status := apiPost(t, srv, tt.apiQueuePath()+"/tickets/"+first.ID+"/cancel", nil, &cancelled); status != http.StatusOK {
		t.Fatalf("cancel = %d, want 200", status)
	}
	if cancelled.Status != "cancelled" || cancelled.Cancellable {
		t.Errorf("cancelled ticket = %+v, want a cancelled, non-cancellable ticket", cancelled)
	}
	if cancelled.Position != nil {
		t.Errorf("position = %d on a cancelled ticket, want null", *cancelled.Position)
	}

	var moved api.Ticket
	apiGet(t, srv, tt.apiQueuePath()+"/tickets/"+second.ID, &moved)
	if moved.Position == nil || *moved.Position != 1 {
		t.Errorf("the customer behind is at position %v, want 1 after the one ahead left", moved.Position)
	}
}

// Cancelling twice is what a customer with a flaky connection does, and it
// must not become an error page for a number that has already gone.
func TestIntegrationAPICancelIsIdempotentForTheHolder(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Cancel Org")

	var ticket api.Ticket
	apiPost(t, srv, tt.apiQueuePath()+"/tickets", api.JoinRequest{CustomerName: "Ada"}, &ticket)

	path := tt.apiQueuePath() + "/tickets/" + ticket.ID + "/cancel"
	if status := apiPost(t, srv, path, nil, nil); status != http.StatusOK {
		t.Fatalf("first cancel = %d, want 200", status)
	}

	var again api.Ticket
	if status := apiPost(t, srv, path, nil, &again); status != http.StatusOK {
		t.Fatalf("second cancel = %d, want 200 — the customer's next question is what their number is doing", status)
	}
	if again.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", again.Status)
	}
}

// The reason the public URL space is shaped the way it is. Org B's slug
// must not reach org A's queue, and the refusal must look exactly like the
// refusal for a queue that does not exist — otherwise probing the URL
// space becomes a directory of who uses the product.
func TestIntegrationAPICannotReachAnotherTenantsQueue(t *testing.T) {
	srv := newTestServer(t)
	a := newTenant(t, srv, "API Tenant A")
	b := newTenant(t, srv, "API Tenant B")

	// Both tenants are seeded with a queue called "Walk-ins", and slugs are
	// unique per org rather than globally — which is the point. Reaching
	// org A's line through org B's slug therefore has to be attempted with
	// a slug only org A has, or the test proves nothing: B's own walk-ins
	// queue would answer, correctly, and the assertion would be about
	// nothing.
	privateQueue, err := queue.Create(context.Background(), a.pool, a.orgID,
		queue.Input{Name: "Tenant A Private Line"})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}

	var ticket api.Ticket
	apiPost(t, srv, a.apiQueuePath()+"/tickets", api.JoinRequest{CustomerName: "Ada"}, &ticket)

	crossTenant := b.apiOrgPath() + "/queues/" + privateQueue.Slug
	nonexistent := b.apiOrgPath() + "/queues/no-such-queue"

	// And org A can reach it, so the 404 below is about the tenant rather
	// than about the queue not existing at all.
	if status := apiGet(t, srv, a.apiOrgPath()+"/queues/"+privateQueue.Slug, nil); status != http.StatusOK {
		t.Fatalf("org A reading its own queue = %d, want 200", status)
	}

	var crossBody, missingBody json.RawMessage
	crossStatus := apiGet(t, srv, crossTenant, &crossBody)
	missingStatus := apiGet(t, srv, nonexistent, &missingBody)

	if crossStatus != http.StatusNotFound {
		t.Fatalf("org B reading org A's queue = %d, want 404", crossStatus)
	}
	if crossStatus != missingStatus || !bytes.Equal(crossBody, missingBody) {
		t.Errorf("another tenant's queue answers differently from a missing one:\n  %d %s\n  %d %s",
			crossStatus, crossBody, missingStatus, missingBody)
	}

	// Nor may org B's slug read, or cancel, a ticket held in org A's line.
	// This one goes through the shared "walk-ins" slug on purpose: B's
	// queue resolves, and the ticket ID from A must still be refused,
	// because a ticket is scoped by the queue it belongs to and not only
	// by the slug that named one.
	ticketPath := b.apiQueuePath() + "/tickets/" + ticket.ID
	if status := apiGet(t, srv, ticketPath, nil); status != http.StatusNotFound {
		t.Errorf("reading another tenant's ticket = %d, want 404", status)
	}
	if status := apiPost(t, srv, ticketPath+"/cancel", nil, nil); status != http.StatusNotFound {
		t.Errorf("cancelling another tenant's ticket = %d, want 404", status)
	}

	// And the ticket is untouched by any of it.
	var still api.Ticket
	apiGet(t, srv, a.apiQueuePath()+"/tickets/"+ticket.ID, &still)
	if still.Status != "waiting" {
		t.Errorf("the ticket's status is now %q — a cross-tenant request changed it", still.Status)
	}
}

// A malformed ID comes out of a URL somebody edited. It must answer 404
// rather than reaching db.MustUUID, which panics by design.
func TestIntegrationAPIMalformedIDsAreNotFoundRatherThanPanics(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Malformed Org")

	for _, path := range []string{
		tt.apiQueuePath() + "/tickets/not-a-uuid",
		tt.apiOrgPath() + "/appointments/not-a-uuid",
	} {
		if status := apiGet(t, srv, path, nil); status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, status)
		}
	}
}

// The API's failures are JSON, including the ones the router produces
// rather than a handler. A front end that gets HTML where it expected an
// envelope reports whatever its parser said, which is never the problem.
func TestIntegrationAPIFailuresAreAlwaysTheErrorEnvelope(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Envelope Org")

	var envelope struct {
		Error api.Error `json:"error"`
	}

	if status := apiGet(t, srv, "/api/v1/orgs/no-such-org", &envelope); status != http.StatusNotFound {
		t.Fatalf("unknown org = %d, want 404", status)
	}
	if envelope.Error.Code != api.CodeNotFound {
		t.Errorf("code = %q, want %q", envelope.Error.Code, api.CodeNotFound)
	}

	if status := apiGet(t, srv, tt.apiOrgPath()+"/nothing-here", &envelope); status != http.StatusNotFound {
		t.Errorf("unrouted path = %d, want 404", status)
	}
	if envelope.Error.Code != api.CodeNotFound {
		t.Errorf("unrouted path code = %q, want %q", envelope.Error.Code, api.CodeNotFound)
	}

	// A validation failure carries the domain's own words, because those
	// are written for a person to read.
	if status := apiPost(t, srv, tt.apiQueuePath()+"/tickets", api.JoinRequest{}, &envelope); status != http.StatusUnprocessableEntity {
		t.Errorf("join with no name = %d, want 422", status)
	}
	if envelope.Error.Code != api.CodeValidation || envelope.Error.Message == "" {
		t.Errorf("validation failure = %+v, want a code and a message worth showing", envelope.Error)
	}
}

// A field name the API does not know is refused rather than ignored. A
// front end sending customerName where the API wants customer_name would
// otherwise book an appointment for nobody and find out from a customer.
func TestIntegrationAPIRejectsUnknownFields(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Unknown Field Org")

	body := map[string]string{"customerName": "Ada"}
	if status := apiPost(t, srv, tt.apiQueuePath()+"/tickets", body, nil); status != http.StatusBadRequest {
		t.Errorf("join with a misspelled field = %d, want 400", status)
	}
}

// The preflight has to be answered against the real router, not only
// against the middleware in isolation — a route registered outside the
// CORS wrapper would pass the unit test and fail every browser.
func TestIntegrationAPIAnswersPreflightThroughTheRealRouter(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Preflight Org")

	req, err := http.NewRequest(http.MethodOptions, srv.URL+tt.apiQueuePath()+"/tickets", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "https://book.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want it unset", got)
	}
}

// The published board endpoint and the new one describe the same queue.
// A business embedding one and a screen reading the other must not be
// shown different numbers.
func TestIntegrationAPIAgreesWithThePublishedBoardEndpoint(t *testing.T) {
	srv := newTestServer(t)
	tt := newTenant(t, srv, "API Agreement Org")

	for _, name := range []string{"Ada", "Grace", "Katherine"} {
		apiPost(t, srv, tt.apiQueuePath()+"/tickets", api.JoinRequest{CustomerName: name}, nil)
	}

	var v1 api.BoardResponse
	apiGet(t, srv, tt.apiQueuePath(), &v1)

	var published map[string]any
	apiGet(t, srv, "/api/q/"+tt.orgSlug+"/"+tt.queueSlug, &published)

	if got := int(published["waiting"].(float64)); got != v1.Queue.Waiting {
		t.Errorf("waiting: published says %d, /api/v1 says %d", got, v1.Queue.Waiting)
	}
	if got := int(published["estimated_wait_minutes"].(float64)); got != v1.Queue.EstimatedWaitMinutes {
		t.Errorf("estimated wait: published says %d, /api/v1 says %d — a website would contradict the sign above the door",
			got, v1.Queue.EstimatedWaitMinutes)
	}
}

// The API's one non-JSON response, end to end: book over JSON, then
// download the booking as a file a calendar can read.
//
// It is worth an integration test rather than only a routing one because
// the thing that would break it is not the file — the appointment package
// tests that — but the response around it: the wrong content type, a
// missing CORS header, or a front end that cannot read the filename.
func TestIntegrationAPIServesTheBookingAsACalendarFile(t *testing.T) {
	srv := newTestServer(t)
	pool := testPool(t)
	bt := newBookingTenant(t, pool, "API Calendar Org")

	var slots struct {
		Slots []struct {
			Token string `json:"token"`
		} `json:"slots"`
	}
	if status := apiGet(t, srv, "/api/v1/orgs/"+bt.orgSlug+"/services/"+bt.serviceSlug+"/slots", &slots); status != http.StatusOK {
		t.Fatalf("slots = %d, want 200", status)
	}
	if len(slots.Slots) == 0 {
		t.Fatal("no slots to book")
	}

	var booked struct {
		ID string `json:"id"`
	}
	if status := apiPost(t, srv, "/api/v1/orgs/"+bt.orgSlug+"/services/"+bt.serviceSlug+"/bookings", map[string]string{
		"slot":           slots.Slots[0].Token,
		"customer_name":  "Katherine Johnson",
		"customer_email": "katherine@example.test",
	}, &booked); status != http.StatusCreated {
		t.Fatalf("booking = %d, want 201", status)
	}

	req, err := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/orgs/"+bt.orgSlug+"/appointments/"+booked.ID+"/calendar.ics", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// A front end on its own domain is the caller this route exists for.
	req.Header.Set("Origin", "https://book.example.com")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET calendar: %v", err)
	}
	defer resp.Body.Close()
	file, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read calendar: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar = %d, want 200\n%s", resp.StatusCode, file)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}
	if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q — a front end on another domain cannot read this", origin)
	}
	// Without this the browser hides the filename from the page that
	// asked for it, and every appointment saves under one invented name.
	if exposed := resp.Header.Get("Access-Control-Expose-Headers"); exposed != "Content-Disposition" {
		t.Errorf("Access-Control-Expose-Headers = %q, want Content-Disposition", exposed)
	}
	if !strings.Contains(string(file), "BEGIN:VEVENT") {
		t.Errorf("the response is not a calendar:\n%s", file)
	}
	// The event points at the server-rendered confirmation page, which
	// works with no build step and no scripting — the right thing for a
	// link opened out of a calendar entry six weeks from now. The file is
	// folded at 75 octets, so the link is looked for the way an importer
	// reads it: unfolded.
	unfolded := strings.ReplaceAll(string(file), "\r\n ", "")
	if !strings.Contains(unfolded, "/book/"+bt.orgSlug+"/a/"+booked.ID) {
		t.Errorf("the event does not link back to the customer's page:\n%s", file)
	}
}
