package appointment

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to Status
		want     bool
	}{
		{StatusBooked, StatusCompleted, true},
		{StatusBooked, StatusCancelled, true},
		{StatusBooked, StatusNoShow, true},

		// Terminal states never reopen: rebooking the freed slot may have
		// already happened, so reviving the old row would double-book it.
		{StatusCancelled, StatusBooked, false},
		{StatusCompleted, StatusBooked, false},
		{StatusNoShow, StatusBooked, false},
		{StatusCompleted, StatusCancelled, false},
		{StatusCancelled, StatusNoShow, false},

		// A no-op transition is still not a valid move.
		{StatusBooked, StatusBooked, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range []string{"booked", "completed", "cancelled", "no_show"} {
		if _, ok := ParseStatus(s); !ok {
			t.Errorf("ParseStatus(%q) rejected a valid status", s)
		}
	}

	// Anything outside the known set must be rejected before it can reach
	// the database.
	for _, s := range []string{"", "BOOKED", "deleted", "'; DROP TABLE appointments;--"} {
		if _, ok := ParseStatus(s); ok {
			t.Errorf("ParseStatus(%q) accepted an invalid status", s)
		}
	}
}
