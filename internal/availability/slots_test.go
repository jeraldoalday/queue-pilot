package availability

import (
	"testing"
	"time"
)

// aMonday is a fixed reference date (2026-01-05 is a Monday) so tests
// don't depend on when they're run.
var aMonday = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

func at(base time.Time, hour, min int) time.Time {
	return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, base.Location())
}

func TestGenerateSlots_NoAvailabilityForWeekday(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Tuesday, StartMin: 9 * 60, EndMin: 17 * 60}}

	slots := GenerateSlots(hours, nil, aMonday, time.Hour, time.Hour)

	if len(slots) != 0 {
		t.Fatalf("got %d slots for a weekday with no availability, want 0", len(slots))
	}
}

func TestGenerateSlots_FullDayOpenNoBookings(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 17 * 60}}

	slots := GenerateSlots(hours, nil, aMonday, time.Hour, time.Hour)

	want := 8 // 9,10,11,12,13,14,15,16 -> each 1hr, last ends at 17:00
	if len(slots) != want {
		t.Fatalf("got %d slots, want %d", len(slots), want)
	}
	if !slots[0].Start.Equal(at(aMonday, 9, 0)) {
		t.Errorf("first slot starts at %v, want 09:00", slots[0].Start)
	}
	last := slots[len(slots)-1]
	if !last.End.Equal(at(aMonday, 17, 0)) {
		t.Errorf("last slot ends at %v, want 17:00", last.End)
	}
}

func TestGenerateSlots_ExistingAppointmentSplitsAvailability(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 12 * 60}}
	existing := []Booking{{Start: at(aMonday, 10, 0), End: at(aMonday, 11, 0)}}

	slots := GenerateSlots(hours, existing, aMonday, time.Hour, time.Hour)

	// Window is 9-12 in 1hr steps: candidates 9-10, 10-11, 11-12.
	// The 10-11 candidate overlaps the booking and must be excluded.
	want := []time.Time{at(aMonday, 9, 0), at(aMonday, 11, 0)}
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d: %+v", len(slots), len(want), slots)
	}
	for i, s := range slots {
		if !s.Start.Equal(want[i]) {
			t.Errorf("slot %d starts at %v, want %v", i, s.Start, want[i])
		}
	}
}

func TestGenerateSlots_BoundaryTouchingBookingIsNotExcluded(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 11 * 60}}
	// Booking occupies exactly the first candidate slot; the second
	// candidate starts exactly when the booking ends and must still be
	// offered (touching, not overlapping).
	existing := []Booking{{Start: at(aMonday, 9, 0), End: at(aMonday, 10, 0)}}

	slots := GenerateSlots(hours, existing, aMonday, time.Hour, time.Hour)

	if len(slots) != 1 {
		t.Fatalf("got %d slots, want 1: %+v", len(slots), slots)
	}
	if !slots[0].Start.Equal(at(aMonday, 10, 0)) {
		t.Errorf("remaining slot starts at %v, want 10:00", slots[0].Start)
	}
}

func TestGenerateSlots_GranularitySmallerThanDuration(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 11 * 60}}

	// 60-min slots offered every 30 min: 9:00-10:00, 9:30-10:30, 10:00-11:00.
	slots := GenerateSlots(hours, nil, aMonday, time.Hour, 30*time.Minute)

	want := []time.Time{at(aMonday, 9, 0), at(aMonday, 9, 30), at(aMonday, 10, 0)}
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d: %+v", len(slots), len(want), slots)
	}
	for i, s := range slots {
		if !s.Start.Equal(want[i]) {
			t.Errorf("slot %d starts at %v, want %v", i, s.Start, want[i])
		}
		if s.End.Sub(s.Start) != time.Hour {
			t.Errorf("slot %d duration = %v, want 1h", i, s.End.Sub(s.Start))
		}
	}
}

func TestGenerateSlots_GranularityLargerThanDuration(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 11 * 60}}

	// 30-min slots offered every 60 min, leaving a gap after each:
	// 9:00-9:30, 10:00-10:30.
	slots := GenerateSlots(hours, nil, aMonday, 30*time.Minute, time.Hour)

	want := []time.Time{at(aMonday, 9, 0), at(aMonday, 10, 0)}
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d: %+v", len(slots), len(want), slots)
	}
	for i, s := range slots {
		if !s.Start.Equal(want[i]) {
			t.Errorf("slot %d starts at %v, want %v", i, s.Start, want[i])
		}
	}
}

func TestGenerateSlots_NoPartialSlotAtWindowEnd(t *testing.T) {
	// Window is 9:00-9:45; a 30-min slot fits once (9:00-9:30). The next
	// candidate (9:30-10:00) would overrun the window and must not appear.
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 9*60 + 45}}

	slots := GenerateSlots(hours, nil, aMonday, 30*time.Minute, 30*time.Minute)

	if len(slots) != 1 {
		t.Fatalf("got %d slots, want 1: %+v", len(slots), slots)
	}
	if !slots[0].End.Equal(at(aMonday, 9, 30)) {
		t.Errorf("slot ends at %v, want 09:30", slots[0].End)
	}
}

func TestGenerateSlots_MultipleWindowsSameDay(t *testing.T) {
	// A split shift: morning and afternoon windows.
	hours := []WeeklyHours{
		{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 11 * 60},
		{Weekday: time.Monday, StartMin: 13 * 60, EndMin: 14 * 60},
	}

	slots := GenerateSlots(hours, nil, aMonday, time.Hour, time.Hour)

	want := []time.Time{at(aMonday, 9, 0), at(aMonday, 10, 0), at(aMonday, 13, 0)}
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d: %+v", len(slots), len(want), slots)
	}
}

func TestGenerateSlots_ZeroDurationReturnsNil(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 17 * 60}}

	slots := GenerateSlots(hours, nil, aMonday, 0, time.Hour)

	if slots != nil {
		t.Fatalf("got %v, want nil for zero duration", slots)
	}
}

func TestGenerateSlots_DefaultsGranularityToDuration(t *testing.T) {
	hours := []WeeklyHours{{Weekday: time.Monday, StartMin: 9 * 60, EndMin: 11 * 60}}

	slots := GenerateSlots(hours, nil, aMonday, time.Hour, 0)

	want := []time.Time{at(aMonday, 9, 0), at(aMonday, 10, 0)}
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d: %+v", len(slots), len(want), slots)
	}
}
