package session

import (
	"context"
	"log/slog"
	"time"
)

// SweepInterval is how often expired sessions are deleted.
//
// It is deliberately slow. Nothing depends on the deletion for
// correctness — an expired session is already refused at lookup, whether
// or not its row still exists — so this is housekeeping, and housekeeping
// that runs every few minutes is a background load on the database for no
// benefit. Six hours removes a day's expiries four times over.
const SweepInterval = 6 * time.Hour

// Sweeper deletes expired session rows in the background.
//
// Sessions expire by being ignored: Get treats a row past its expiry as
// absent. That is what makes expiry correct, and it is also why nothing
// ever removed the rows — an instance with a year of logins behind it
// carries a sessions table that is mostly dead weight, slowing every
// index scan on it and growing without bound.
type Sweeper struct {
	store    deleter
	log      *slog.Logger
	interval time.Duration
}

// deleter is the one thing the sweeper needs from a Store. Naming it
// keeps the loop — start now, repeat, log, stop on cancel — testable
// without a database, which is where the behaviour worth testing is.
type deleter interface {
	DeleteExpired(context.Context) (int64, error)
}

func NewSweeper(store *Store, log *slog.Logger) *Sweeper {
	return &Sweeper{store: store, log: log, interval: SweepInterval}
}

// Run sweeps until ctx is cancelled, then returns.
//
// It sweeps once at startup rather than waiting out the first interval,
// so a process that is restarted more often than the interval still
// cleans up — otherwise a deploy every few hours would mean the sweep
// never runs at all.
//
// A failed sweep is logged and the loop continues. There is nothing to
// recover from: the rows are still there, they are still ignored by every
// lookup, and the next tick will try again. Stopping the loop over a
// transient database error would turn a temporary problem into a
// permanent one.
func (s *Sweeper) Run(ctx context.Context) {
	s.sweep(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	// Bounded independently of ctx's own lifetime so a sweep cannot hold a
	// connection indefinitely if the database is wedged; the next tick
	// will pick the work up.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	removed, err := s.store.DeleteExpired(ctx)
	if err != nil {
		// Cancellation during shutdown is not a failure worth reporting.
		if ctx.Err() != nil {
			return
		}
		s.log.Error("session sweep failed", "error", err)
		return
	}
	if removed > 0 {
		s.log.Info("expired sessions removed", "count", removed)
	}
}
