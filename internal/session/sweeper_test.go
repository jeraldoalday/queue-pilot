package session

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingStore stands in for the database: it records how often the
// sweeper asked for a deletion, which is the behaviour under test — the
// deletion itself is one SQL statement and belongs to the integration
// tests.
type countingStore struct {
	mu      sync.Mutex
	calls   int
	removed int64
	err     error
	fired   chan struct{}
}

func (c *countingStore) DeleteExpired(context.Context) (int64, error) {
	c.mu.Lock()
	c.calls++
	removed, err := c.removed, c.err
	c.mu.Unlock()

	select {
	case c.fired <- struct{}{}:
	default:
	}
	return removed, err
}

func (c *countingStore) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// sweeperFor builds a sweeper over the fake store, bypassing NewSweeper so
// the test can drive the interval.
func sweeperFor(store deleter, log *slog.Logger, interval time.Duration) *Sweeper {
	return &Sweeper{store: store, log: log, interval: interval}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

// syncBuffer collects log output written by the sweeper's goroutine and
// read by the test's. A plain bytes.Buffer is a data race across that
// boundary, and -race is the point of running these.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// The first sweep must not wait out an interval. A process redeployed
// more often than the interval would otherwise never sweep at all.
func TestSweeperRunsImmediately(t *testing.T) {
	store := &countingStore{fired: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sweeperFor(store, discardLogger(), time.Hour).Run(ctx)

	select {
	case <-store.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweeper did not sweep at startup")
	}
}

func TestSweeperRepeatsOnItsInterval(t *testing.T) {
	store := &countingStore{fired: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sweeperFor(store, discardLogger(), 10*time.Millisecond).Run(ctx)

	for i := 0; i < 3; i++ {
		select {
		case <-store.fired:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d sweeps ran, want at least %d", store.callCount(), i+1)
		}
	}
}

// Cancelling the context is how the process shuts the sweeper down, and
// it has to actually stop rather than leak a goroutine ticking against a
// closed pool.
func TestSweeperStopsWhenTheContextIsCancelled(t *testing.T) {
	store := &countingStore{fired: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		sweeperFor(store, discardLogger(), 5*time.Millisecond).Run(ctx)
		close(done)
	}()

	<-store.fired
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	// And it must stay stopped rather than tick once more on the way out.
	settled := store.callCount()
	time.Sleep(50 * time.Millisecond)
	if got := store.callCount(); got != settled {
		t.Errorf("the sweeper ran %d more times after being stopped", got-settled)
	}
}

// A failing sweep is logged and the loop continues. The rows are still
// there and still ignored by every lookup, so stopping over a transient
// database error would turn a temporary problem into a permanent one.
func TestSweeperKeepsGoingAfterAFailure(t *testing.T) {
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	store := &countingStore{fired: make(chan struct{}, 1), err: context.DeadlineExceeded}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sweeperFor(store, log, 5*time.Millisecond).Run(ctx)

	for i := 0; i < 3; i++ {
		select {
		case <-store.fired:
		case <-time.After(2 * time.Second):
			t.Fatal("the sweeper stopped after a failure")
		}
	}
	if !strings.Contains(buf.String(), "session sweep failed") {
		t.Error("a failed sweep was not logged")
	}
}

// A sweep that removed nothing says nothing. On a quiet instance this
// runs four times a day, and a line every time saying "removed 0" is
// noise that trains people to skip the log.
func TestSweeperOnlyLogsWhenItRemovedSomething(t *testing.T) {
	var quiet syncBuffer
	store := &countingStore{fired: make(chan struct{}, 1), removed: 0}
	ctx, cancel := context.WithCancel(context.Background())
	go sweeperFor(store, slog.New(slog.NewTextHandler(&quiet, nil)), time.Hour).Run(ctx)
	<-store.fired
	cancel()

	if strings.Contains(quiet.String(), "expired sessions removed") {
		t.Error("a sweep that removed nothing still wrote a log line")
	}

	var loud syncBuffer
	busy := &countingStore{fired: make(chan struct{}, 1), removed: 12}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go sweeperFor(busy, slog.New(slog.NewTextHandler(&loud, nil)), time.Hour).Run(ctx2)
	<-busy.fired
	// Give the log write a moment to land before reading the buffer.
	time.Sleep(50 * time.Millisecond)
	cancel2()

	if !strings.Contains(loud.String(), "expired sessions removed") {
		t.Error("a sweep that removed rows wrote no log line")
	}
}

// The interval is housekeeping, not a correctness deadline: expiry is
// enforced at lookup whether or not the row is gone. A short interval
// would be background load on the database for no benefit.
func TestSweepIntervalIsSlow(t *testing.T) {
	if SweepInterval < time.Hour {
		t.Errorf("SweepInterval = %v, want something measured in hours", SweepInterval)
	}
}
