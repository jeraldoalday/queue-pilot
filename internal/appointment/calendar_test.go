package appointment

import (
	"strings"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
	"github.com/jeraldoalday/queuepilot/internal/platform/icalendar"
)

const (
	testApptID = "3f4d1a2b-0000-4000-8000-000000000001"
	testOrgID  = "3f4d1a2b-0000-4000-8000-000000000002"
)

var renderedAt = time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

func testOrg() sqlc.Organization {
	return sqlc.Organization{
		ID:       db.MustUUID(testOrgID),
		Name:     "Acme Dental",
		Slug:     "acme-dental",
		Timezone: "Asia/Manila",
	}
}

func testAppointment(status Status) sqlc.GetAppointmentWithNamesRow {
	manila := time.FixedZone("PST", 8*3600)
	return sqlc.GetAppointmentWithNamesRow{
		ID:            db.MustUUID(testApptID),
		OrgID:         db.MustUUID(testOrgID),
		CustomerName:  "Jo Rivera",
		CustomerEmail: "jo@example.test",
		StartsAt:      db.Timestamptz(time.Date(2026, 3, 10, 10, 0, 0, 0, manila)),
		EndsAt:        db.Timestamptz(time.Date(2026, 3, 10, 10, 30, 0, 0, manila)),
		Status:        string(status),
		CreatedAt:     db.Timestamptz(time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)),
		UpdatedAt:     db.Timestamptz(time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)),
		ServiceName:   "Cleaning",
		StaffName:     "Dana Cruz",
	}
}

const testPageURL = "https://queuepilot.test/book/acme-dental/a/" + testApptID

func line(t *testing.T, out, name string) string {
	t.Helper()
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	for _, l := range strings.Split(unfolded, "\r\n") {
		if strings.HasPrefix(l, name+":") {
			return strings.TrimPrefix(l, name+":")
		}
	}
	t.Fatalf("no %s in:\n%s", name, out)
	return ""
}

func TestAppointmentCalendarCarriesTheBooking(t *testing.T) {
	cal := AppointmentCalendar(testOrg(), testAppointment(StatusBooked), testPageURL)
	out := cal.Render(renderedAt)

	// The UID has to be stable and unique: it is the only thing tying a
	// re-download to the copy already sitting in somebody's calendar.
	if got := line(t, out, "UID"); got != testApptID+"@queuepilot" {
		t.Errorf("UID = %q", got)
	}
	// 10:00 in Manila is 02:00 UTC. A customer reading this in another
	// country should see the clinic's 10:00 rendered in their own zone,
	// which only works if the instant on the wire is absolute.
	if got := line(t, out, "DTSTART"); got != "20260310T020000Z" {
		t.Errorf("DTSTART = %q, want 10:00 Manila as 02:00 UTC", got)
	}
	if got := line(t, out, "DTEND"); got != "20260310T023000Z" {
		t.Errorf("DTEND = %q", got)
	}
	if got := line(t, out, "SUMMARY"); got != "Cleaning at Acme Dental" {
		t.Errorf("SUMMARY = %q", got)
	}
	if got := line(t, out, "LOCATION"); got != "Acme Dental" {
		t.Errorf("LOCATION = %q", got)
	}
	if got := line(t, out, "URL"); got != testPageURL {
		t.Errorf("URL = %q, want the confirmation page", got)
	}
	if got := line(t, out, "DESCRIPTION"); !strings.Contains(got, "Dana Cruz") {
		t.Errorf("DESCRIPTION = %q, want the staff member named", got)
	}
	// The customer cancels from the page, so the link has to survive into
	// clients that show the description but not the URL property.
	if got := line(t, out, "DESCRIPTION"); !strings.Contains(got, testPageURL) {
		t.Errorf("DESCRIPTION = %q, want the link repeated in the body", got)
	}
}

// The file must not carry a customer's name or email. It is downloaded by
// the person it belongs to, so their own name would be harmless — but the
// same builder feeds the API, and a payload that never carries personal
// data cannot leak it through a route that later forgets to check.
func TestAppointmentCalendarOmitsTheCustomersDetails(t *testing.T) {
	out := AppointmentCalendar(testOrg(), testAppointment(StatusBooked), testPageURL).Render(renderedAt)

	for _, secret := range []string{"Jo Rivera", "jo@example.test"} {
		if strings.Contains(out, secret) {
			t.Errorf("the calendar file carries %q:\n%s", secret, out)
		}
	}
}

// Two downloads of an unchanged booking have to be the same file. A stamp
// taken from the clock would make every download look like an update to a
// client comparing copies.
func TestAppointmentCalendarIsStableAcrossDownloads(t *testing.T) {
	appt := testAppointment(StatusBooked)
	first := AppointmentCalendar(testOrg(), appt, testPageURL).Render(renderedAt)
	later := AppointmentCalendar(testOrg(), appt, testPageURL).Render(renderedAt.Add(3 * time.Hour))

	if first != later {
		t.Error("the same unchanged booking rendered two different files")
	}
	if got := line(t, first, "DTSTAMP"); got != "20260301T060000Z" {
		t.Errorf("DTSTAMP = %q, want the booking's own last change", got)
	}
}

func TestCalendarState(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		want     icalendar.Status
		method   string
		sequence int
	}{
		{"a live booking is confirmed", StatusBooked, icalendar.StatusConfirmed, icalendar.MethodPublish, 0},
		{"a cancelled one withdraws the event", StatusCancelled, icalendar.StatusCancelled, icalendar.MethodCancel, 1},
		{
			// The business distinguishes a no-show from a cancellation;
			// a phone has no use for the distinction, and leaving a
			// missed appointment sitting there as confirmed is wrong.
			name:     "a missed one is cancelled too",
			status:   StatusNoShow,
			want:     icalendar.StatusCancelled,
			method:   icalendar.MethodCancel,
			sequence: 1,
		},
		{"a completed one stays on the calendar", StatusCompleted, icalendar.StatusConfirmed, icalendar.MethodPublish, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, method, sequence := calendarState(tt.status)
			if status != tt.want || method != tt.method || sequence != tt.sequence {
				t.Errorf("calendarState(%s) = (%s, %s, %d), want (%s, %s, %d)",
					tt.status, status, method, sequence, tt.want, tt.method, tt.sequence)
			}
		})
	}
}

// A cancellation issued at the same sequence as the confirmation may be
// ignored as a duplicate, which leaves an appointment on somebody's phone
// that no longer exists.
func TestCancelledBookingOutranksTheConfirmationItReplaces(t *testing.T) {
	booked := AppointmentCalendar(testOrg(), testAppointment(StatusBooked), testPageURL).Render(renderedAt)
	cancelled := AppointmentCalendar(testOrg(), testAppointment(StatusCancelled), testPageURL).Render(renderedAt)

	if line(t, booked, "UID") != line(t, cancelled, "UID") {
		t.Fatal("the cancellation names a different event from the booking")
	}
	if got := line(t, cancelled, "SEQUENCE"); got != "1" {
		t.Errorf("cancelled SEQUENCE = %q, want it above the booking's 0", got)
	}
	if got := line(t, cancelled, "STATUS"); got != "CANCELLED" {
		t.Errorf("cancelled STATUS = %q", got)
	}
}

func TestCalendarFilename(t *testing.T) {
	manila, err := time.LoadLocation("Asia/Manila")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}

	appt := testAppointment(StatusBooked)
	if got := CalendarFilename(testOrg(), appt, manila); got != "acme-dental-2026-03-10.ics" {
		t.Errorf("CalendarFilename = %q", got)
	}

	// The day is the org's, not whatever zone the file is read in. This
	// booking is the 10th in Manila and the evening of the 9th twelve
	// hours west of UTC, and the customer thinks of it as the day the
	// business told them.
	west := time.FixedZone("AoE", -12*3600)
	if got := CalendarFilename(testOrg(), appt, west); got != "acme-dental-2026-03-09.ics" {
		t.Errorf("CalendarFilename(UTC-12) = %q, want the 9th", got)
	}

	// An org whose name slugifies to nothing still needs a filename.
	unnameable := testOrg()
	unnameable.Name = "待ち行列"
	if got := CalendarFilename(unnameable, appt, manila); !strings.HasPrefix(got, "appointment-") {
		t.Errorf("CalendarFilename for an unsluggable name = %q", got)
	}
}

func TestCalendarURL(t *testing.T) {
	if got := calendarURL("acme-dental", testApptID); got != "/book/acme-dental/a/"+testApptID+"/calendar.ics" {
		t.Errorf("calendarURL = %q", got)
	}
}
