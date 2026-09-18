// Package availability computes bookable appointment slots from a staff
// member's recurring weekly working hours and their existing appointments.
// GenerateSlots is a pure function deliberately kept free of any DB or
// context dependency — everything about slot correctness is testable
// without a database.
package availability

import "time"

// WeeklyHours is one recurring open window for a staff member, e.g.
// "Monday 09:00-17:00". StartMin/EndMin are minutes since midnight,
// matching how staff_availability stores start_time/end_time.
type WeeklyHours struct {
	Weekday  time.Weekday
	StartMin int
	EndMin   int
}

// Booking is an existing appointment to avoid overlapping.
type Booking struct {
	Start, End time.Time
}

type Slot struct {
	Start, End time.Time
}

// GenerateSlots returns candidate appointment slots on `date` for a staff
// member whose recurring weekly hours are `hours`, excluding any slot that
// would overlap an entry in `existing`. Each slot is `duration` long;
// candidate start times advance by `granularity` (defaulting to duration
// if granularity <= 0, per the "step = service duration" default).
//
// A slot is only returned if it fits entirely within its weekly window —
// no partial slot is ever offered at the end of a window.
func GenerateSlots(hours []WeeklyHours, existing []Booking, date time.Time, duration, granularity time.Duration) []Slot {
	if duration <= 0 {
		return nil
	}
	if granularity <= 0 {
		granularity = duration
	}

	var slots []Slot
	for _, w := range hours {
		if w.Weekday != date.Weekday() {
			continue
		}

		// Built directly from the wall-clock minute-of-day rather than as
		// midnight plus an elapsed duration: on a day the location springs
		// forward or falls back, adding StartMin/EndMin minutes to midnight
		// counts real elapsed time, not clock time, and lands an hour off
		// from the working hours staff actually set. time.Date interprets
		// StartMin/EndMin (which may run past 59) as wall-clock minutes and
		// normalizes them within the correct offset for that day.
		winStart := time.Date(date.Year(), date.Month(), date.Day(), 0, w.StartMin, 0, 0, date.Location())
		winEnd := time.Date(date.Year(), date.Month(), date.Day(), 0, w.EndMin, 0, 0, date.Location())

		for start := winStart; !start.Add(duration).After(winEnd); start = start.Add(granularity) {
			end := start.Add(duration)
			if !overlapsAny(start, end, existing) {
				slots = append(slots, Slot{Start: start, End: end})
			}
		}
	}
	return slots
}

// overlapsAny reports whether [start, end) overlaps any existing booking.
// Half-open interval comparison means a slot that merely touches an
// existing booking's boundary (e.g. starts exactly when another ends) does
// not count as overlapping.
func overlapsAny(start, end time.Time, existing []Booking) bool {
	for _, b := range existing {
		if start.Before(b.End) && b.Start.Before(end) {
			return true
		}
	}
	return false
}

// OverlapsWeeklyHours reports whether [startMin, endMin) on weekday overlaps
// any window already in hours for that same weekday.
//
// A split shift — separate morning and afternoon windows on one weekday —
// is a real schedule (TestGenerateSlots_MultipleWindowsSameDay covers it),
// so this only rejects windows that genuinely overlap, not merely coexist.
// Two windows that touch at a boundary (one ending exactly when the other
// starts) do not overlap, matching overlapsAny's own half-open comparison.
//
// It exists because GenerateSlots has no notion of "the same slot twice":
// it walks every window independently, so two overlapping recurring windows
// for one staff member produce the same start time once per window that
// covers it — which the admin's manual booking form then renders as
// identical, unlabelled duplicate time buttons.
func OverlapsWeeklyHours(hours []WeeklyHours, weekday time.Weekday, startMin, endMin int) bool {
	for _, h := range hours {
		if h.Weekday != weekday {
			continue
		}
		if startMin < h.EndMin && h.StartMin < endMin {
			return true
		}
	}
	return false
}
