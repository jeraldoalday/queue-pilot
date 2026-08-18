package queue

import "testing"

// What a customer may withdraw is narrower than what staff may do, and the
// difference is the point: this pins which statuses offer the button.
func TestCustomerCancellable(t *testing.T) {
	tests := []struct {
		status Status
		want   bool
		why    string
	}{
		{StatusWaiting, true, "still in line and free to go"},
		{StatusCalled, true, "called but not yet at the counter"},
		{StatusServing, false, "already at the counter; staff resolve it"},
		{StatusDone, false, "terminal"},
		{StatusNoShow, false, "terminal"},
		{StatusCancelled, false, "terminal"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := CustomerCancellable(tt.status); got != tt.want {
				t.Errorf("CustomerCancellable(%s) = %v, want %v (%s)", tt.status, got, tt.want, tt.why)
			}
		})
	}
}

// Every status must have a decided answer, so a status added to the
// lifecycle later cannot quietly default to "customers may cancel it".
func TestCustomerCancellableCoversEveryStatus(t *testing.T) {
	allowed := 0
	for _, s := range AllStatuses() {
		if CustomerCancellable(s) {
			allowed++
			if IsTerminal(s) {
				t.Errorf("CustomerCancellable(%s) is true for a terminal status", s)
			}
		}
	}
	if allowed != 2 {
		t.Errorf("%d statuses are customer-cancellable, want exactly waiting and called", allowed)
	}
}

// A customer cancellation must be a move the shared lifecycle already
// allows — the public page is a second door onto the same state machine,
// not a way around it.
func TestCustomerCancellableIsASubsetOfTheLifecycle(t *testing.T) {
	for _, s := range AllStatuses() {
		if CustomerCancellable(s) && !CanTransition(s, StatusCancelled) {
			t.Errorf("customers may cancel from %s, but the lifecycle forbids %s -> cancelled", s, s)
		}
	}
}

func TestTicketCancelURL(t *testing.T) {
	const (
		orgSlug   = "acme"
		queueSlug = "walk-ins"
		ticketID  = "0d6f4c1e-2b3a-4d5e-8f90-112233445566"
	)

	want := "/q/acme/walk-ins/t/" + ticketID + "/cancel"
	if got := ticketCancelURL(orgSlug, queueSlug, ticketID); got != want {
		t.Errorf("ticketCancelURL = %q, want %q", got, want)
	}

	// The cancel URL must sit under the ticket's own URL, because that is
	// what scopes it: the handler resolves the same org and queue from the
	// same path segments before it touches anything.
	base := ticketURL(orgSlug, queueSlug, ticketID)
	if got := ticketCancelURL(orgSlug, queueSlug, ticketID); got[:len(base)] != base {
		t.Errorf("ticketCancelURL %q is not under the ticket URL %q", got, base)
	}
}
