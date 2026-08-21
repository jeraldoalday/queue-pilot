package appointment

import (
	"errors"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/availability"
)

// day is a fixed reference day for the slot tests. A real date rather than
// a zero time, so weekday-sensitive code downstream behaves as it would in
// production.
var day = time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func slot(startHour, startMin, endHour, endMin int) availability.Slot {
	return availability.Slot{Start: at(startHour, startMin), End: at(endHour, endMin)}
}

// wideOpen is a window that excludes nothing, for the tests that are about
// merging rather than about the booking window.
func wideOpen() (time.Time, time.Time) {
	return day.Add(-24 * time.Hour), day.Add(48 * time.Hour)
}

func TestMergeStaffSlotsCollapsesTheSameTimeOfferedTwice(t *testing.T) {
	from, to := wideOpen()
	merged := MergeStaffSlots([]StaffSlots{
		{StaffUserID: "u-bea", StaffName: "Bea", Slots: []availability.Slot{slot(9, 0, 9, 30), slot(10, 0, 10, 30)}},
		{StaffUserID: "u-ada", StaffName: "Ada", Slots: []availability.Slot{slot(9, 0, 9, 30)}},
	}, from, to)

	if len(merged) != 2 {
		t.Fatalf("merged %d slots, want 2 — 09:00 was offered by two people and must appear once", len(merged))
	}
	if !merged[0].Start.Equal(at(9, 0)) || !merged[1].Start.Equal(at(10, 0)) {
		t.Fatalf("merged slots are out of order: %v, %v", merged[0].Start, merged[1].Start)
	}
	// The customer is never asked to pick a person, so the app picks — and
	// it must pick the same one every time the page renders, or the
	// confirmation names someone the customer never saw.
	if merged[0].StaffName != "Ada" {
		t.Errorf("09:00 was assigned to %q; the tie must break by name so the assignment is stable", merged[0].StaffName)
	}
	if merged[1].StaffName != "Bea" {
		t.Errorf("10:00 was assigned to %q, want Bea — she is the only one free then", merged[1].StaffName)
	}
}

// The database returns staff in whatever order it likes. If that order
// reached the assignment, reloading the booking page could hand the same
// slot to a different person between render and submit.
func TestMergeStaffSlotsIsIndependentOfInputOrder(t *testing.T) {
	from, to := wideOpen()
	ada := StaffSlots{StaffUserID: "u-ada", StaffName: "Ada", Slots: []availability.Slot{slot(9, 0, 9, 30)}}
	bea := StaffSlots{StaffUserID: "u-bea", StaffName: "Bea", Slots: []availability.Slot{slot(9, 0, 9, 30)}}

	first := MergeStaffSlots([]StaffSlots{ada, bea}, from, to)
	second := MergeStaffSlots([]StaffSlots{bea, ada}, from, to)

	if first[0].StaffUserID != second[0].StaffUserID {
		t.Errorf("09:00 went to %q in one ordering and %q in the other", first[0].StaffUserID, second[0].StaffUserID)
	}
}

func TestMergeStaffSlotsDropsSlotsOutsideTheWindow(t *testing.T) {
	merged := MergeStaffSlots([]StaffSlots{
		{StaffUserID: "u-ada", StaffName: "Ada", Slots: []availability.Slot{
			slot(8, 0, 8, 30),   // before the window opens
			slot(9, 0, 9, 30),   // inside
			slot(17, 0, 17, 30), // after it closes
		}},
	}, at(9, 0), at(16, 0))

	if len(merged) != 1 {
		t.Fatalf("merged %d slots, want only the one inside the window", len(merged))
	}
	if !merged[0].Start.Equal(at(9, 0)) {
		t.Errorf("kept %v, want 09:00", merged[0].Start)
	}
}

// The window's near edge is what keeps a slot beginning in four minutes
// off the page. Its exact boundary matters: a slot starting exactly at the
// lead time is offerable, and one a minute earlier is not.
func TestMergeStaffSlotsKeepsTheSlotExactlyAtTheWindowEdge(t *testing.T) {
	merged := MergeStaffSlots([]StaffSlots{
		{StaffUserID: "u-ada", StaffName: "Ada", Slots: []availability.Slot{slot(9, 0, 9, 30), slot(9, 30, 10, 0)}},
	}, at(9, 0), at(9, 30))

	if len(merged) != 2 {
		t.Fatalf("merged %d slots, want both edges kept", len(merged))
	}
}

func TestMergeStaffSlotsHandlesNobodyAvailable(t *testing.T) {
	from, to := wideOpen()
	if got := MergeStaffSlots(nil, from, to); len(got) != 0 {
		t.Errorf("merged %d slots from no staff at all", len(got))
	}
	if got := MergeStaffSlots([]StaffSlots{{StaffUserID: "u-ada", StaffName: "Ada"}}, from, to); len(got) != 0 {
		t.Errorf("merged %d slots from a staff member with no free times", len(got))
	}
}

func TestCheckBookingWindow(t *testing.T) {
	now := at(12, 0)

	tests := []struct {
		name  string
		start time.Time
		want  error
	}{
		{"already started", at(11, 0), ErrTooSoon},
		{"inside the lead time", now.Add(MinLeadTime - time.Minute), ErrTooSoon},
		{"exactly at the lead time", now.Add(MinLeadTime), nil},
		{"comfortably ahead", now.Add(48 * time.Hour), nil},
		{"at the far edge", now.Add(MaxBookAhead), nil},
		{"beyond the far edge", now.Add(MaxBookAhead + time.Minute), ErrTooFarAhead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckBookingWindow(tt.start, now); !errors.Is(err, tt.want) {
				t.Errorf("CheckBookingWindow(%v) = %v, want %v", tt.start, err, tt.want)
			}
		})
	}
}

// The page and the booking endpoint must agree about what is offerable.
// They read the window through the same function precisely so a slot the
// page shows is never one the endpoint then refuses.
func TestBookingWindowAgreesWithTheCheck(t *testing.T) {
	now := at(12, 0)
	earliest, latest := BookingWindow(now)

	if err := CheckBookingWindow(earliest, now); err != nil {
		t.Errorf("the earliest slot the page would offer is refused by the check: %v", err)
	}
	if err := CheckBookingWindow(latest, now); err != nil {
		t.Errorf("the latest slot the page would offer is refused by the check: %v", err)
	}
}

func TestFindSlotPrefersTheStaffMemberTheFormNamed(t *testing.T) {
	offered := []OpenSlot{
		{Start: at(9, 0), End: at(9, 30), StaffUserID: "u-ada", StaffName: "Ada"},
		{Start: at(10, 0), End: at(10, 30), StaffUserID: "u-bea", StaffName: "Bea"},
	}

	got, ok := findSlot(offered, at(10, 0), "u-bea")
	if !ok {
		t.Fatal("the submitted slot was not found among the offered ones")
	}
	if got.StaffUserID != "u-bea" {
		t.Errorf("booked with %q, want the person the page showed", got.StaffUserID)
	}
}

// A form can name a staff member who was free when the page rendered and
// is not free now. The customer asked for a time, and the time is still
// open with somebody else — booking it is a better answer than refusing.
func TestFindSlotFallsBackToWhoeverIsFreeAtThatTime(t *testing.T) {
	offered := []OpenSlot{{Start: at(9, 0), End: at(9, 30), StaffUserID: "u-ada", StaffName: "Ada"}}

	got, ok := findSlot(offered, at(9, 0), "u-gone")
	if !ok {
		t.Fatal("a slot that is still open was reported as unavailable")
	}
	if got.StaffUserID != "u-ada" {
		t.Errorf("booked with %q, want the person who is actually free", got.StaffUserID)
	}
}

func TestFindSlotRejectsATimeNobodyOffered(t *testing.T) {
	offered := []OpenSlot{{Start: at(9, 0), End: at(9, 30), StaffUserID: "u-ada", StaffName: "Ada"}}

	if _, ok := findSlot(offered, at(3, 0), "u-ada"); ok {
		t.Error("03:00 was accepted; a hand-edited form must not book outside working hours")
	}
	if _, ok := findSlot(nil, at(9, 0), "u-ada"); ok {
		t.Error("a slot was found in an empty offer list")
	}
}

func TestLooksLikeEmail(t *testing.T) {
	valid := []string{"a@b.co", "someone@example.test", " padded@example.test "}
	for _, s := range valid {
		if !looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = false, want true", s)
		}
	}

	invalid := []string{"", "nobody", "@example.test", "someone@", "two words@example.test", "a@b\nc"}
	for _, s := range invalid {
		if looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = true, want false", s)
		}
	}
}
