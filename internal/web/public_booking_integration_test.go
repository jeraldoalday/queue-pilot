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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/appointment"
	"github.com/jeraldoalday/queuepilot/internal/auth"
	"github.com/jeraldoalday/queuepilot/internal/availability"
	"github.com/jeraldoalday/queuepilot/internal/catalog"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/id"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// testPool opens a pool for the tests that seed data directly rather than
// through the HTTP surface. The server has its own; sharing one would mean
// a test closing it out from under the handlers.
func testPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// bookingTenant is an org set up the way a business would have to set
// itself up before it could take a booking online: a service, somebody who
// performs it, and hours for them to be free in.
type bookingTenant struct {
	orgID       string
	orgSlug     string
	serviceSlug string
	serviceID   string
	staffID     string
}

// newBookingTenant seeds that setup, giving the staff member the same
// working window on every day of the week. A schedule that only covered
// one weekday would make the test's result depend on which day it ran.
func newBookingTenant(t *testing.T, pool *pgxpool.Pool, name string) bookingTenant {
	t.Helper()

	ctx := context.Background()
	sessions := session.NewStore(pool, clock.Real{})

	ident, err := auth.Signup(ctx, pool, sessions, auth.SignupInput{
		OrgName:    name + " " + id.New(),
		AdminName:  "Booking Admin",
		AdminEmail: "admin-" + id.New() + "@example.test",
		Password:   "password123",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	svc, err := catalog.CreateService(ctx, pool, ident.OrgID, catalog.ServiceInput{
		Name:            "Consultation",
		DurationMinutes: 30,
		Active:          true,
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	staff, err := auth.CreateStaffUser(ctx, pool, ident.OrgID, auth.CreateStaffInput{
		Name:     "Ada Lovelace",
		Email:    "staff-" + id.New() + "@example.test",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}

	serviceID := db.UUIDString(svc.ID)
	staffID := db.UUIDString(staff.ID)
	if err := catalog.ToggleStaffAssignment(ctx, pool, ident.OrgID, serviceID, staffID); err != nil {
		t.Fatalf("assign staff to service: %v", err)
	}

	// Midnight to midnight, every day: whenever the test runs, and
	// whatever the booking window's near edge lands on, there is a slot.
	for weekday := 0; weekday <= 6; weekday++ {
		if _, err := availability.AddHours(ctx, pool, ident.OrgID, staffID, availability.HoursInput{
			Weekday:  weekday,
			StartMin: 0,
			EndMin:   24 * 60,
		}); err != nil {
			t.Fatalf("add hours: %v", err)
		}
	}

	orgRow, err := sqlc.New(pool).GetOrganizationByID(ctx, db.MustUUID(ident.OrgID))
	if err != nil {
		t.Fatalf("read org: %v", err)
	}

	return bookingTenant{
		orgID:       ident.OrgID,
		orgSlug:     orgRow.Slug,
		serviceSlug: svc.Slug,
		serviceID:   serviceID,
		staffID:     staffID,
	}
}

func (bt bookingTenant) bookPath() string {
	return "/book/" + bt.orgSlug + "/" + bt.serviceSlug
}

// slotValues pulls the slot tokens out of a rendered picker. Reading them
// off the page rather than computing them is the point: it proves the
// value the page offers is one the booking endpoint accepts, which is the
// join the two halves could most easily get wrong.
var slotValues = regexp.MustCompile(`name="slot"\s+value="([^"]+)"`)

func offeredSlots(t *testing.T, body string) []string {
	t.Helper()

	var out []string
	for _, m := range slotValues.FindAllStringSubmatch(body, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// postForm submits without following redirects, so a test can read the
// Location the handler set.
func postForm(t *testing.T, srv *httptest.Server, path string, form url.Values) (int, string, string) {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

// The whole customer path in one test, because each step only means
// anything if the one before it produced what it claimed: the service list
// links to the picker, the picker's own slot value is accepted by the
// booking endpoint, and the booking's redirect lands on a page the
// customer can come back to.
func TestIntegrationPublicBookingFlow(t *testing.T) {
	srv := newTestServer(t)
	pool := testPool(t)
	bt := newBookingTenant(t, pool, "Booking Flow Org")

	status, body := get(t, srv, "/book/"+bt.orgSlug)
	if status != http.StatusOK {
		t.Fatalf("service list = %d, want 200", status)
	}
	if !strings.Contains(body, "Consultation") {
		t.Fatal("the service list did not offer the bookable service")
	}
	if !strings.Contains(body, bt.bookPath()) {
		t.Fatal("the service list did not link to the service's picker")
	}

	status, body = get(t, srv, bt.bookPath())
	if status != http.StatusOK {
		t.Fatalf("slot picker = %d, want 200", status)
	}
	slots := offeredSlots(t, body)
	if len(slots) == 0 {
		t.Fatal("the picker offered no times for a staff member available all day")
	}

	status, location, body := postForm(t, srv, bt.bookPath(), url.Values{
		"slot":           {slots[len(slots)-1]},
		"customer_name":  {"Grace Hopper"},
		"customer_email": {"grace@example.test"},
	})
	if status != http.StatusSeeOther {
		t.Fatalf("booking = %d, want 303 — booking must redirect so a refresh doesn't book twice.\n%s", status, body)
	}
	if !strings.HasPrefix(location, "/book/"+bt.orgSlug+"/a/") {
		t.Fatalf("booking redirected to %q, want the customer's own confirmation page", location)
	}

	status, body = get(t, srv, location)
	if status != http.StatusOK {
		t.Fatalf("confirmation = %d, want 200", status)
	}
	for _, want := range []string{"Grace Hopper", "Consultation", "Ada Lovelace", "Confirmed"} {
		if !strings.Contains(body, want) {
			t.Errorf("the confirmation page is missing %q", want)
		}
	}

	// Cancelling is the customer's own action, and it must leave them on a
	// page that shows the new state rather than on a re-postable form.
	status, cancelLocation, _ := postForm(t, srv, location+"/cancel", url.Values{})
	if status != http.StatusSeeOther {
		t.Fatalf("cancel = %d, want 303", status)
	}
	if cancelLocation != location {
		t.Fatalf("cancel redirected to %q, want back to %q", cancelLocation, location)
	}

	status, body = get(t, srv, location)
	if status != http.StatusOK {
		t.Fatalf("confirmation after cancelling = %d, want 200", status)
	}
	if !strings.Contains(body, "Cancelled") {
		t.Error("the page does not show the booking as cancelled")
	}
	if strings.Contains(body, "Cancel this appointment") {
		t.Error("the page still offers to cancel an already-cancelled booking")
	}

	// The cancelled slot must go back on offer. A cancellation that freed
	// nothing would be worse than none: the customer believes they have
	// released the time, and nobody else can take it.
	_, body = get(t, srv, bt.bookPath())
	if got := offeredSlots(t, body); len(got) < len(slots) {
		t.Errorf("after cancelling, %d slots are offered where %d were before", len(got), len(slots))
	}
}

// The form is entirely under the customer's control, so the endpoint must
// not believe it. These are the edits worth trying.
func TestIntegrationPublicBookingRefusesTimesItNeverOffered(t *testing.T) {
	srv := newTestServer(t)
	pool := testPool(t)
	bt := newBookingTenant(t, pool, "Booking Guard Org")

	_, body := get(t, srv, bt.bookPath())
	slots := offeredSlots(t, body)
	if len(slots) == 0 {
		t.Fatal("no slots to work from")
	}

	// Every one of these is a value the picker never rendered. The
	// endpoint has to re-derive the day's slots and refuse them rather
	// than take the form's word for it.
	//
	// The org's timezone is UTC here (a fresh signup's default), so the
	// times are built in UTC to match what the picker would have offered.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	offGrid := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 3, 17, 0, 0, time.UTC)
	farFuture := time.Now().UTC().Add(appointment.MaxBookAhead + 48*time.Hour)

	cases := map[string]string{
		// Between two slots: the schedule is open, but nothing starts here.
		"a time that is not on the half-hour grid": offGrid.Format(time.RFC3339) + "|" + bt.staffID,
		"a time beyond the booking window":         farFuture.Format(time.RFC3339) + "|" + bt.staffID,
		"a malformed token":                        "not-a-time|" + bt.staffID,
		"nothing at all":                           "",
	}

	for name, slot := range cases {
		t.Run(name, func(t *testing.T) {
			status, _, body := postForm(t, srv, bt.bookPath(), url.Values{
				"slot":           {slot},
				"customer_name":  {"Mallory"},
				"customer_email": {"mallory@example.test"},
			})
			if status == http.StatusSeeOther {
				t.Fatalf("%s was accepted and booked", name)
			}
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("%s = %d, want 422 with the picker re-rendered.\n%s", name, status, body)
			}
		})
	}
}

// A booking is one org's data, reachable only through that org's URL —
// the same rule the public ticket page holds, checked here because the
// booking pages resolve their tenant the same way and could regress
// independently.
func TestIntegrationPublicBookingIsScopedToItsOrg(t *testing.T) {
	srv := newTestServer(t)
	pool := testPool(t)
	orgA := newBookingTenant(t, pool, "Booking Org A")
	orgB := newBookingTenant(t, pool, "Booking Org B")

	_, body := get(t, srv, orgA.bookPath())
	slots := offeredSlots(t, body)
	if len(slots) == 0 {
		t.Fatal("no slots to work from")
	}

	_, location, _ := postForm(t, srv, orgA.bookPath(), url.Values{
		"slot":           {slots[0]},
		"customer_name":  {"Ada"},
		"customer_email": {"ada@example.test"},
	})
	appointmentID := location[strings.LastIndex(location, "/")+1:]

	// Org B's URL, org A's real appointment ID.
	if status, _ := get(t, srv, "/book/"+orgB.orgSlug+"/a/"+appointmentID); status != http.StatusNotFound {
		t.Errorf("org B reading org A's booking = %d, want 404", status)
	}
	// And org B must not be able to cancel it either — a read-only leak
	// would be bad, a write is worse.
	if status, _, _ := postForm(t, srv, "/book/"+orgB.orgSlug+"/a/"+appointmentID+"/cancel", url.Values{}); status != http.StatusNotFound {
		t.Errorf("org B cancelling org A's booking = %d, want 404", status)
	}
	// It is still readable where it belongs, so the 404s above are
	// isolation rather than a broken link.
	if status, _ := get(t, srv, location); status != http.StatusOK {
		t.Errorf("the booking is unreadable on its own org's URL = %d, want 200", status)
	}
}

// A service an org has taken off its list must not stay bookable for
// anyone who kept the link.
func TestIntegrationInactiveServiceIsNotBookable(t *testing.T) {
	srv := newTestServer(t)
	pool := testPool(t)
	bt := newBookingTenant(t, pool, "Retired Service Org")

	if status, _ := get(t, srv, bt.bookPath()); status != http.StatusOK {
		t.Fatalf("picker before deactivation = %d, want 200", status)
	}

	if _, err := catalog.UpdateService(context.Background(), pool, bt.orgID, bt.serviceID, catalog.ServiceInput{
		Name:            "Consultation",
		DurationMinutes: 30,
		Active:          false,
	}); err != nil {
		t.Fatalf("deactivate service: %v", err)
	}

	if status, _ := get(t, srv, bt.bookPath()); status != http.StatusNotFound {
		t.Errorf("picker for a deactivated service = %d, want 404", status)
	}
	_, body := get(t, srv, "/book/"+bt.orgSlug)
	if strings.Contains(body, "Consultation") {
		t.Error("the service list still offers a deactivated service")
	}
}
