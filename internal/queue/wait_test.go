package queue

import (
	"testing"
	"time"
)

func TestServiceTimeMeasured(t *testing.T) {
	tests := []struct {
		name string
		st   ServiceTime
		want bool
	}{
		{"enough samples", ServiceTime{Average: 8 * time.Minute, Samples: MinServiceSamples}, true},
		{"one sample short", ServiceTime{Average: 8 * time.Minute, Samples: MinServiceSamples - 1}, false},
		{"no history at all", ServiceTime{}, false},
		{
			// A queue whose tickets were all completed in the same instant
			// they started (a staff member clicking through) averages zero.
			// Trusting that would tell every customer they have no wait.
			name: "plenty of samples but a zero average",
			st:   ServiceTime{Average: 0, Samples: 100},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.Measured(); got != tt.want {
				t.Fatalf("Measured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServiceTimePerCustomerFallsBackUntilMeasured(t *testing.T) {
	fresh := ServiceTime{Average: 30 * time.Minute, Samples: 1}
	if got := fresh.PerCustomer(); got != FallbackServiceTime {
		t.Fatalf("PerCustomer() with 1 sample = %v, want the fallback %v", got, FallbackServiceTime)
	}

	settled := ServiceTime{Average: 30 * time.Minute, Samples: MinServiceSamples}
	if got := settled.PerCustomer(); got != 30*time.Minute {
		t.Fatalf("PerCustomer() with enough samples = %v, want the measured 30m", got)
	}
}

func TestEstimate(t *testing.T) {
	measured := func(d time.Duration) ServiceTime {
		return ServiceTime{Average: d, Samples: MinServiceSamples}
	}

	tests := []struct {
		name    string
		ahead   int
		st      ServiceTime
		servers int
		want    time.Duration
	}{
		{
			name:    "nobody ahead means no wait",
			ahead:   0,
			st:      measured(10 * time.Minute),
			servers: 1,
			want:    0,
		},
		{
			name:    "a negative count is treated as an empty line",
			ahead:   -3,
			st:      measured(10 * time.Minute),
			servers: 1,
			want:    0,
		},
		{
			name:    "one counter clears people one at a time",
			ahead:   3,
			st:      measured(10 * time.Minute),
			servers: 1,
			want:    30 * time.Minute,
		},
		{
			// Three counters clear four people in two rounds, not four.
			name:    "extra counters divide the wait",
			ahead:   4,
			st:      measured(10 * time.Minute),
			servers: 3,
			want:    20 * time.Minute,
		},
		{
			name:    "a partial round still costs a full round",
			ahead:   5,
			st:      measured(10 * time.Minute),
			servers: 4,
			want:    20 * time.Minute,
		},
		{
			name:    "estimates round up to the next five minutes",
			ahead:   1,
			st:      measured(7 * time.Minute),
			servers: 1,
			want:    10 * time.Minute,
		},
		{
			name:    "an exact multiple is left alone",
			ahead:   2,
			st:      measured(15 * time.Minute),
			servers: 1,
			want:    30 * time.Minute,
		},
		{
			name:    "no history uses the fallback service time",
			ahead:   2,
			st:      ServiceTime{},
			servers: 1,
			want:    2 * FallbackServiceTime,
		},
		{
			// A queue with nobody at the counters still has to answer the
			// question; zero servers must not divide by zero.
			name:    "zero servers is treated as one",
			ahead:   2,
			st:      measured(10 * time.Minute),
			servers: 0,
			want:    20 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Estimate(tt.ahead, tt.st, tt.servers); got != tt.want {
				t.Fatalf("Estimate(%d, %v, %d) = %v, want %v", tt.ahead, tt.st, tt.servers, got, tt.want)
			}
		})
	}
}

// An estimate must never shrink as more people join the line ahead of you,
// and must never grow as counters are added. Stated as a property because
// a rounding change is exactly the kind of edit that could break it while
// every example above still passes.
func TestEstimateIsMonotonic(t *testing.T) {
	st := ServiceTime{Average: 7 * time.Minute, Samples: MinServiceSamples}

	for servers := 1; servers <= 4; servers++ {
		prev := time.Duration(-1)
		for ahead := 0; ahead <= 40; ahead++ {
			got := Estimate(ahead, st, servers)
			if got < prev {
				t.Fatalf("Estimate fell from %v to %v as the line grew (ahead=%d, servers=%d)", prev, got, ahead, servers)
			}
			prev = got
		}
	}

	for ahead := 1; ahead <= 20; ahead++ {
		prev := time.Duration(1<<62 - 1)
		for servers := 1; servers <= 8; servers++ {
			got := Estimate(ahead, st, servers)
			if got > prev {
				t.Fatalf("Estimate rose from %v to %v as counters were added (ahead=%d, servers=%d)", prev, got, ahead, servers)
			}
			prev = got
		}
	}
}

func TestPosition(t *testing.T) {
	if got := Position(0); got != 1 {
		t.Fatalf("Position(0) = %d, want 1 — the front of the line is first, not zeroth", got)
	}
	if got := Position(4); got != 5 {
		t.Fatalf("Position(4) = %d, want 5", got)
	}
}

func TestFormatWait(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "You're next"},
		{-time.Minute, "You're next"},
		{2 * time.Minute, "Less than 5 minutes"},
		{5 * time.Minute, "About 5 minutes"},
		{45 * time.Minute, "About 45 minutes"},
		{90 * time.Minute, "About an hour"},
		{3 * time.Hour, "Over 3 hours"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FormatWait(tt.d); got != tt.want {
				t.Fatalf("FormatWait(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// The join-form wording addresses someone who does not hold a ticket yet,
// so a zero wait must read as "nothing to wait behind" rather than as the
// ticket-holder's "you're next".
func TestFormatJoinWait(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "No wait right now"},
		{-time.Minute, "No wait right now"},
		{2 * time.Minute, "Less than 5 minutes"},
		{20 * time.Minute, "About 20 minutes"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FormatJoinWait(tt.d); got != tt.want {
				t.Fatalf("FormatJoinWait(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
