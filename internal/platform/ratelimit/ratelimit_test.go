package ratelimit

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
)

// movable is a Clock the test advances by hand, so rate-limit behaviour
// over time is verified without any sleeping.
type movable struct{ t time.Time }

func (m *movable) Now() time.Time { return m.t }

func (m *movable) advance(d time.Duration) { m.t = m.t.Add(d) }

func newTestClock() *movable {
	return &movable{t: time.Date(2026, time.March, 1, 9, 0, 0, 0, time.UTC)}
}

func TestAllowsBurstThenRefuses(t *testing.T) {
	c := newTestClock()
	l := New(3, time.Minute, c)

	for i := 1; i <= 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d of the burst was refused", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("a fourth request was allowed with a burst of 3")
	}
}

func TestRefillsOverTime(t *testing.T) {
	c := newTestClock()
	l := New(2, time.Minute, c)

	l.Allow("ip")
	l.Allow("ip")
	if l.Allow("ip") {
		t.Fatal("burst was not exhausted")
	}

	// Half an interval is not yet a whole token.
	c.advance(30 * time.Second)
	if l.Allow("ip") {
		t.Fatal("a partial token was spent")
	}

	c.advance(30 * time.Second)
	if !l.Allow("ip") {
		t.Fatal("a token should have refilled after a full interval")
	}
}

// An idle client must not bank an unbounded allowance: coming back after
// an hour should grant the burst, not sixty requests.
func TestRefillIsCappedAtBurst(t *testing.T) {
	c := newTestClock()
	l := New(2, time.Minute, c)

	l.Allow("ip")
	l.Allow("ip")

	c.advance(time.Hour)

	if !l.Allow("ip") || !l.Allow("ip") {
		t.Fatal("the burst should be available again after a long idle period")
	}
	if l.Allow("ip") {
		t.Fatal("an idle client banked more than the burst")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	c := newTestClock()
	l := New(1, time.Minute, c)

	if !l.Allow("a") {
		t.Fatal("first request from a was refused")
	}
	if l.Allow("a") {
		t.Fatal("second request from a should be refused")
	}
	if !l.Allow("b") {
		t.Fatal("b was penalised for a's traffic")
	}
}

func TestRetryReportsTheWait(t *testing.T) {
	c := newTestClock()
	l := New(1, time.Minute, c)

	if got := l.Retry("unseen"); got != 0 {
		t.Fatalf("Retry for an unseen key = %v, want 0", got)
	}

	l.Allow("ip")
	if got := l.Retry("ip"); got != time.Minute {
		t.Fatalf("Retry right after spending the last token = %v, want 1m", got)
	}

	c.advance(45 * time.Second)
	if got := l.Retry("ip"); got != 15*time.Second {
		t.Fatalf("Retry after 45s of a 1m interval = %v, want 15s", got)
	}

	c.advance(15 * time.Second)
	if got := l.Retry("ip"); got != 0 {
		t.Fatalf("Retry once a token is available = %v, want 0", got)
	}
}

// The bucket map must not grow one entry per stranger forever — on a
// public endpoint that is a memory leak driven by other people.
func TestStaleBucketsAreSwept(t *testing.T) {
	c := newTestClock()
	l := New(2, time.Minute, c)

	for _, key := range []string{"a", "b", "c"} {
		l.Allow(key)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("tracked keys = %d, want 3", got)
	}

	// Long enough for every bucket to have refilled completely, and past
	// the sweep interval. The next call triggers the sweep.
	c.advance(sweepInterval + time.Hour)
	l.Allow("d")

	if got := l.Len(); got != 1 {
		t.Fatalf("tracked keys after the sweep = %d, want only the live one", got)
	}
	// Sweeping must not have cost anyone their standing.
	if !l.Allow("a") {
		t.Fatal("a forgotten key should behave like a first-time visitor")
	}
}

func TestNewClampsNonsenseSettings(t *testing.T) {
	c := newTestClock()

	zeroBurst := New(0, time.Minute, c)
	if !zeroBurst.Allow("ip") {
		t.Fatal("a burst of 0 was taken literally, refusing everything")
	}

	zeroInterval := New(1, 0, c)
	zeroInterval.Allow("ip")
	if got := zeroInterval.Retry("ip"); got != time.Second {
		t.Fatalf("a zero interval should fall back to 1s, got a retry of %v", got)
	}
}

func TestClientKey(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		trust      bool
		want       string
	}{
		{
			name:       "the socket address by default",
			remoteAddr: "203.0.113.7:54321",
			forwarded:  "198.51.100.1",
			want:       "203.0.113.7",
		},
		{
			// The whole point of the flag: a header any client can set
			// must not become the key on a directly-exposed server, or
			// every visitor can mint unlimited identities.
			name:       "a forwarded header is ignored unless trusted",
			remoteAddr: "203.0.113.7:54321",
			forwarded:  "attacker-chosen",
			want:       "203.0.113.7",
		},
		{
			name:       "the forwarded client when trusted",
			remoteAddr: "10.0.0.1:443",
			forwarded:  "198.51.100.1",
			trust:      true,
			want:       "198.51.100.1",
		},
		{
			name:       "the left-most entry of a proxy chain",
			remoteAddr: "10.0.0.1:443",
			forwarded:  "198.51.100.1, 70.41.3.18, 10.0.0.9",
			trust:      true,
			want:       "198.51.100.1",
		},
		{
			name:       "falls back to the socket when the header is empty",
			remoteAddr: "10.0.0.1:443",
			forwarded:  "",
			trust:      true,
			want:       "10.0.0.1",
		},
		{
			name:       "an address without a port is used as-is",
			remoteAddr: "unix",
			want:       "unix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := ClientKey(r, tt.trust); got != tt.want {
				t.Fatalf("ClientKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	c := newTestClock()
	l := New(1, time.Minute, c)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	var served int
	handler := Middleware(l, false, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))

	do := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/q/acme/walk-ins/tickets", nil)
		r.RemoteAddr = "203.0.113.7:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if got := do().Code; got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}

	second := do()
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want %q", got, "60")
	}
	if served != 1 {
		t.Fatalf("the handler ran %d times, want 1 — a limited request reached it", served)
	}

	c.advance(time.Minute)
	if got := do().Code; got != http.StatusOK {
		t.Fatalf("request after the interval = %d, want 200", got)
	}
}

// The limiter is shared by every public request, so concurrent access must
// be safe. Run with -race, this fails loudly if the locking is wrong.
func TestConcurrentAllowIsSafe(t *testing.T) {
	l := New(100, time.Millisecond, clock.Real{})

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				l.Allow("shared")
				l.Retry("shared")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
