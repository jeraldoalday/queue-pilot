package queue

import (
	"fmt"
	"time"
)

// MinServiceSamples is how many completed tickets a queue needs before its
// measured average is used for estimates. Below this, one unusually long
// or short customer would dominate, and a confident "about 40 minutes"
// drawn from a single sample is worse than saying nothing.
const MinServiceSamples = 5

// FallbackServiceTime is used while a queue is still below
// MinServiceSamples. It is only ever combined with a "roughly" wording in
// the UI, never presented as measured.
const FallbackServiceTime = 10 * time.Minute

// waitRounding is the granularity every estimate is rounded up to. A wait
// shown as "about 12 minutes" implies a precision nothing here has;
// rounding to 5 makes the estimate honest about being an estimate.
const waitRounding = 5 * time.Minute

// ServiceTime is a queue's measured hands-on service duration and the
// number of completed tickets it was averaged over.
type ServiceTime struct {
	Average time.Duration
	Samples int
}

// Measured reports whether enough tickets have completed for Average to be
// worth trusting.
func (s ServiceTime) Measured() bool {
	return s.Samples >= MinServiceSamples && s.Average > 0
}

// PerCustomer returns the duration to use for one customer: the measured
// average once there is enough history, and the fallback until then.
func (s ServiceTime) PerCustomer() time.Duration {
	if s.Measured() {
		return s.Average
	}
	return FallbackServiceTime
}

// Estimate returns how long a customer with `ahead` people still in front
// of them can expect to wait, given the queue's service time and how many
// counters are working through it.
//
// The model is deliberately the simple one: with `servers` counters each
// taking PerCustomer per person, the people ahead are cleared in
// ceil(ahead/servers) rounds. It ignores the partially-served customer at
// each counter, which makes the number slightly optimistic — the right
// direction to be wrong in is the one that doesn't promise less waiting
// than reality, so the result is rounded *up* to waitRounding to
// compensate.
//
// servers below 1 is treated as 1: a queue with nobody serving has no
// meaningful estimate, and dividing by zero is not an answer.
func Estimate(ahead int, st ServiceTime, servers int) time.Duration {
	if ahead <= 0 {
		return 0
	}
	if servers < 1 {
		servers = 1
	}

	rounds := (ahead + servers - 1) / servers // ceil(ahead/servers)
	return roundUp(time.Duration(rounds)*st.PerCustomer(), waitRounding)
}

// Position is the customer's place in line: the people ahead of them plus
// themselves. A ticket at the front is position 1, never 0.
func Position(ahead int) int {
	return ahead + 1
}

// FormatWait renders an estimate for a customer. It never shows a bare
// "0 minutes" — being next in line is a statement about position, and the
// caller shows that instead.
//
// Past an hour the wait is spelled out in hours *and* minutes rather than
// collapsed to a phrase. An earlier version said "About an hour" for
// anything under two, which told someone facing a 1h55m wait that they
// had about an hour — an understatement of nearly an hour, and in the one
// direction Estimate deliberately avoids being wrong in. A customer who
// leaves and comes back on that basis has lost their place in the line.
//
// The estimate is already rounded up to five minutes, so the extra digits
// claim no more precision than the number behind them has.
func FormatWait(d time.Duration) string {
	switch {
	case d <= 0:
		return "You're next"
	case d < waitRounding:
		return "Less than 5 minutes"
	case d < time.Hour:
		return fmt.Sprintf("About %d minutes", int(d.Minutes()))
	}

	hours := int(d / time.Hour)
	minutes := int(d%time.Hour) / int(time.Minute)
	if minutes == 0 {
		return fmt.Sprintf("About %d %s", hours, plural(hours, "hour", "hours"))
	}
	return fmt.Sprintf("About %d %s %d %s",
		hours, plural(hours, "hour", "hours"),
		minutes, plural(minutes, "minute", "minutes"))
}

// FormatJoinWait renders an estimate for someone deciding whether to join
// the line at all. It is worded differently from FormatWait on purpose:
// that one addresses a customer who already holds a ticket, so a zero
// there means "you're next", while a zero here means there is nobody to
// wait behind.
func FormatJoinWait(d time.Duration) string {
	if d <= 0 {
		return "No wait right now"
	}
	return FormatWait(d)
}

// roundUp rounds d up to the next multiple of to. An exact multiple is
// left alone.
func roundUp(d, to time.Duration) time.Duration {
	if to <= 0 || d%to == 0 {
		return d
	}
	return (d/to + 1) * to
}
