package web

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/queue"
	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

// noon is the instant every dashboard test treats as "now", so "still to
// come" has a fixed meaning across them.
var noon = time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)

func line(name string, stats queue.Stats) QueueLine {
	return QueueLine{
		Queue: sqlc.Queue{ID: db.MustUUID(uuidFor(name)), Name: name, Active: true},
		Stats: stats,
	}
}

// uuidFor turns a test's queue name into a stable UUID, since the template
// compares IDs to decide which line is the longest.
func uuidFor(name string) string {
	const base = "00000000-0000-0000-0000-0000000000"
	return base + string("0123456789abcdef"[len(name)%16]) + "0"
}

func appt(status string, start time.Time) sqlc.ListAppointmentsForOrgOnDayRow {
	return sqlc.ListAppointmentsForOrgOnDayRow{
		Status:       status,
		StartsAt:     pgtype.Timestamptz{Time: start, Valid: true},
		CustomerName: "Customer at " + start.Format("15:04"),
	}
}

func TestDashboardTotalsAddUpAcrossQueues(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Lines: []QueueLine{
			line("Front desk", queue.Stats{Waiting: 3, Called: 1, Serving: 2, Done: 7}),
			line("Pharmacy", queue.Stats{Waiting: 4, Serving: 1, Done: 2, NoShow: 1}),
		},
	}

	if got := d.Waiting(); got != 7 {
		t.Errorf("Waiting() = %d, want 7", got)
	}
	// Called and serving both occupy a counter — the manager's question is
	// how many customers are being dealt with, not which stage they are at.
	if got := d.InProgress(); got != 4 {
		t.Errorf("InProgress() = %d, want 4", got)
	}
	if got := d.ServedToday(); got != 9 {
		t.Errorf("ServedToday() = %d, want 9", got)
	}
}

func TestDashboardLongestLine(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Lines: []QueueLine{
			line("Front desk", queue.Stats{Waiting: 2}),
			line("Pharmacy", queue.Stats{Waiting: 9}),
			line("Returns", queue.Stats{Waiting: 1}),
		},
	}

	longest, ok := d.LongestLine()
	if !ok {
		t.Fatal("no longest line reported for an org with people waiting")
	}
	if longest.Queue.Name != "Pharmacy" {
		t.Errorf("longest line = %q, want Pharmacy", longest.Queue.Name)
	}
}

// A queue with nobody in it must not be pointed at as "the longest line".
// Highlighting an empty queue would send a spare member of staff to stand
// at a counter with no one at it.
func TestDashboardLongestLineIsSilentWhenNobodyIsWaiting(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Lines: []QueueLine{
			line("Front desk", queue.Stats{Done: 4}),
			line("Pharmacy", queue.Stats{Serving: 1}),
		},
	}

	if longest, ok := d.LongestLine(); ok {
		t.Errorf("LongestLine() named %q with nobody waiting anywhere", longest.Queue.Name)
	}
	if _, ok := (Dashboard{Now: noon}).LongestLine(); ok {
		t.Error("LongestLine() named a queue for an org that has none")
	}
}

func TestDashboardNextAppointmentSkipsWhatHasAlreadyPassed(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Appointments: []sqlc.ListAppointmentsForOrgOnDayRow{
			appt("booked", noon.Add(-90*time.Minute)),
			appt("booked", noon.Add(2*time.Hour)),
			appt("booked", noon.Add(30*time.Minute)),
		},
	}

	next, ok := d.NextAppointment()
	if !ok {
		t.Fatal("no next appointment reported when two are still ahead")
	}
	if want := noon.Add(30 * time.Minute); !db.Time(next.StartsAt).Equal(want) {
		t.Errorf("next appointment starts %v, want %v", db.Time(next.StartsAt), want)
	}
}

// Cancelled and completed bookings are not somebody to prepare for. A
// cancelled one showing as "next up" would have staff waiting for a
// customer who told them they were not coming.
func TestDashboardNextAppointmentIgnoresResolvedBookings(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Appointments: []sqlc.ListAppointmentsForOrgOnDayRow{
			appt("cancelled", noon.Add(15*time.Minute)),
			appt("no_show", noon.Add(20*time.Minute)),
			appt("completed", noon.Add(25*time.Minute)),
			appt("booked", noon.Add(45*time.Minute)),
		},
	}

	next, ok := d.NextAppointment()
	if !ok {
		t.Fatal("no next appointment reported when one is still booked")
	}
	if want := noon.Add(45 * time.Minute); !db.Time(next.StartsAt).Equal(want) {
		t.Errorf("next appointment starts %v, want %v", db.Time(next.StartsAt), want)
	}
	if got := d.Booked(); got != 1 {
		t.Errorf("Booked() = %d, want 1 — only bookings still expected count", got)
	}
}

func TestDashboardNextAppointmentIsSilentOnAFinishedDay(t *testing.T) {
	d := Dashboard{
		Now: noon,
		Appointments: []sqlc.ListAppointmentsForOrgOnDayRow{
			appt("booked", noon.Add(-2*time.Hour)),
			appt("cancelled", noon.Add(time.Hour)),
		},
	}

	if next, ok := d.NextAppointment(); ok {
		t.Errorf("NextAppointment() returned one starting %v, want none", db.Time(next.StartsAt))
	}
}

func TestDashboardQuiet(t *testing.T) {
	tests := []struct {
		name string
		d    Dashboard
		want bool
	}{
		{
			name: "nothing at all",
			d:    Dashboard{Now: noon, Lines: []QueueLine{line("Front desk", queue.Stats{Done: 12})}},
			want: true,
		},
		{
			name: "someone waiting",
			d:    Dashboard{Now: noon, Lines: []QueueLine{line("Front desk", queue.Stats{Waiting: 1})}},
			want: false,
		},
		{
			name: "someone at a counter",
			d:    Dashboard{Now: noon, Lines: []QueueLine{line("Front desk", queue.Stats{Serving: 1})}},
			want: false,
		},
		{
			name: "a booking still ahead",
			d: Dashboard{Now: noon, Appointments: []sqlc.ListAppointmentsForOrgOnDayRow{
				appt("booked", noon.Add(time.Hour)),
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Quiet(); got != tt.want {
				t.Errorf("Quiet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "appointment", "appointments"); got != "appointment" {
		t.Errorf("plural(1) = %q", got)
	}
	for _, n := range []int{0, 2, 17} {
		if got := plural(n, "appointment", "appointments"); got != "appointments" {
			t.Errorf("plural(%d) = %q", n, got)
		}
	}
}

// The dashboard prints the two addresses a business has to know: where
// customers book, and where they walk in. Both are one line of a page
// nobody reads twice, so the test is here rather than in a template.
func TestDashboardSurfacesBothPublicAddresses(t *testing.T) {
	d := Dashboard{
		Role:    tenant.RoleAdmin,
		OrgSlug: "acme-dental",
		Lines: []QueueLine{
			{Queue: sqlc.Queue{Name: "Walk-ins", Slug: "walk-ins"}},
			{Queue: sqlc.Queue{Name: "Prescriptions", Slug: "prescriptions"}},
		},
		Bookable: 2,
	}

	page := renderDashboard(t, d)

	for _, want := range []string{"/q/acme-dental", "/book/acme-dental", "2 lines open"} {
		if !strings.Contains(page, want) {
			t.Errorf("the dashboard does not show %q", want)
		}
	}
}

// An org with nothing open is told so, rather than being handed an
// address that answers with an empty page.
func TestDashboardSaysWhenThereIsNoWalkInAddressWorthSharing(t *testing.T) {
	page := renderDashboard(t, Dashboard{Role: tenant.RoleAdmin, OrgSlug: "acme-dental"})

	if !strings.Contains(page, "No walk-in lines are open") {
		t.Errorf("an org with no open lines is not told so:\n%s", page)
	}
	if !strings.Contains(page, "/app/queues") {
		t.Error("an admin is not offered the page that would fix it")
	}
}

// A staff member sees the same floor and none of the configuration
// shortcuts: a link to a page that would answer them with a 403 is worse
// than no link.
func TestDashboardDoesNotOfferStaffTheAdminFix(t *testing.T) {
	page := renderDashboard(t, Dashboard{Role: tenant.RoleStaff, OrgSlug: "acme-dental"})

	if strings.Contains(page, "/app/queues") {
		t.Error("a staff member is offered the admin-only queue configuration")
	}
}

func renderDashboard(t *testing.T, d Dashboard) string {
	t.Helper()

	var buf bytes.Buffer
	if err := AppHome(d).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}
