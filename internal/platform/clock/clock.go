// Package clock abstracts wall-clock time so business logic (session
// expiry, slot generation, queue timestamps) stays testable without
// sleeping or relying on the real system clock.
package clock

import "time"

type Clock interface {
	Now() time.Time
}

// Real is the Clock used in production: plain time.Now().
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Frozen is a Clock for tests that always returns a fixed instant.
type Frozen struct{ T time.Time }

func (f Frozen) Now() time.Time { return f.T }
