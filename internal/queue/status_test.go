package queue

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name string
		from Status
		to   Status
		want bool
	}{
		{"waiting to called: the number goes up", StatusWaiting, StatusCalled, true},
		{"called to serving: the customer arrived", StatusCalled, StatusServing, true},
		{"called back to waiting: give them another turn", StatusCalled, StatusWaiting, true},
		{"called to no-show: nobody came", StatusCalled, StatusNoShow, true},
		{"serving to done: finished", StatusServing, StatusDone, true},

		// A customer can walk out at any point before the service ends.
		{"waiting to cancelled", StatusWaiting, StatusCancelled, true},
		{"called to cancelled", StatusCalled, StatusCancelled, true},
		{"serving to cancelled", StatusServing, StatusCancelled, true},

		// Someone at the counter has, by definition, shown up.
		{"serving to no-show is nonsense", StatusServing, StatusNoShow, false},

		// Skipping the call step would leave called_at unstamped, so the
		// wait statistics would silently lose that ticket.
		{"waiting straight to serving skips the call", StatusWaiting, StatusServing, false},
		{"waiting straight to done skips everything", StatusWaiting, StatusDone, false},

		// End states are terminal: the board has moved past that number.
		{"done cannot reopen", StatusDone, StatusServing, false},
		{"no-show cannot reopen", StatusNoShow, StatusWaiting, false},
		{"cancelled cannot reopen", StatusCancelled, StatusWaiting, false},

		{"a status is never a transition to itself", StatusWaiting, StatusWaiting, false},
		{"an unknown source status allows nothing", Status("bogus"), StatusCalled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestIsTerminalAndIsOpen(t *testing.T) {
	tests := []struct {
		status       Status
		wantTerminal bool
		wantOpen     bool
	}{
		{StatusWaiting, false, true},
		{StatusCalled, false, true},
		{StatusServing, false, true},
		{StatusDone, true, false},
		{StatusNoShow, true, false},
		{StatusCancelled, true, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := IsTerminal(tt.status); got != tt.wantTerminal {
				t.Errorf("IsTerminal(%q) = %v, want %v", tt.status, got, tt.wantTerminal)
			}
			if got := IsOpen(tt.status); got != tt.wantOpen {
				t.Errorf("IsOpen(%q) = %v, want %v", tt.status, got, tt.wantOpen)
			}
		})
	}
}

// Every open status must be non-terminal and vice versa. Stated as a
// property so adding a status later can't leave the two views of the
// lifecycle disagreeing.
func TestOpenAndTerminalArePartitions(t *testing.T) {
	for _, s := range AllStatuses() {
		if IsOpen(s) == IsTerminal(s) {
			t.Errorf("status %q is both open and terminal (or neither)", s)
		}
	}
}

func TestParseStatus(t *testing.T) {
	for _, s := range AllStatuses() {
		got, ok := ParseStatus(string(s))
		if !ok || got != s {
			t.Errorf("ParseStatus(%q) = (%q, %v), want (%q, true)", s, got, ok, s)
		}
	}

	for _, bad := range []string{"", "WAITING", "deleted", "waiting ", "'; DROP TABLE queue_tickets --"} {
		if _, ok := ParseStatus(bad); ok {
			t.Errorf("ParseStatus(%q) accepted an unknown status", bad)
		}
	}
}

func TestLabelCoversEveryStatus(t *testing.T) {
	for _, s := range AllStatuses() {
		label := Label(s)
		if label == "" || label == string(s) {
			t.Errorf("Label(%q) = %q, want a human-readable label", s, label)
		}
	}
}
