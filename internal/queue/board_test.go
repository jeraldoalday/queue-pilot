package queue

import (
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

func ticket(number int32, status Status) sqlc.QueueTicket {
	return sqlc.QueueTicket{
		ID:     pgtype.UUID{Valid: true},
		Number: number,
		Status: string(status),
	}
}

func TestStatsTotals(t *testing.T) {
	s := Stats{Waiting: 3, Called: 1, Serving: 2, Done: 10, NoShow: 1, Cancelled: 2}
	if got := s.Issued(); got != 19 {
		t.Errorf("Issued() = %d, want 19", got)
	}
	if got := s.Open(); got != 6 {
		t.Errorf("Open() = %d, want 6", got)
	}
}

func TestBoardActiveServers(t *testing.T) {
	empty := Board{}
	if got := empty.ActiveServers(); got != 1 {
		t.Errorf("ActiveServers() on an idle queue = %d, want the floor of 1", got)
	}

	busy := Board{InProgress: []sqlc.QueueTicket{ticket(1, StatusServing), ticket(2, StatusCalled)}}
	if got := busy.ActiveServers(); got != 2 {
		t.Errorf("ActiveServers() = %d, want 2", got)
	}
}

func TestBoardNowServing(t *testing.T) {
	if _, ok := (Board{}).NowServing(); ok {
		t.Error("NowServing() reported a ticket on an idle queue")
	}

	b := Board{InProgress: []sqlc.QueueTicket{ticket(4, StatusServing), ticket(7, StatusCalled)}}
	got, ok := b.NowServing()
	if !ok || got.Number != 4 {
		t.Errorf("NowServing() = (#%d, %v), want (#4, true)", got.Number, ok)
	}
}

func TestBoardAheadOf(t *testing.T) {
	b := Board{Waiting: []sqlc.QueueTicket{ticket(3, StatusWaiting), ticket(5, StatusWaiting), ticket(9, StatusWaiting)}}

	tests := []struct {
		number int32
		want   int
	}{
		{3, 0}, // the front of the waiting list has nobody ahead
		{5, 1},
		{9, 2},
		{12, 3}, // a ticket not yet in the list is behind everyone
		{1, 0},  // a number below the list is ahead of it all
	}

	for _, tt := range tests {
		if got := b.AheadOf(tt.number); got != tt.want {
			t.Errorf("AheadOf(#%d) = %d, want %d", tt.number, got, tt.want)
		}
	}
}

// Tickets already called are not counted as ahead: they have left the
// waiting list and are at a counter, so counting them would inflate every
// remaining customer's estimate for the whole time a service takes.
func TestBoardAheadOfIgnoresInProgressTickets(t *testing.T) {
	b := Board{
		Waiting:    []sqlc.QueueTicket{ticket(5, StatusWaiting)},
		InProgress: []sqlc.QueueTicket{ticket(3, StatusServing), ticket(4, StatusCalled)},
	}
	if got := b.AheadOf(5); got != 0 {
		t.Fatalf("AheadOf(#5) = %d, want 0", got)
	}
}

func TestBoardEstimateFor(t *testing.T) {
	b := Board{
		Waiting: []sqlc.QueueTicket{
			ticket(1, StatusWaiting), ticket(2, StatusWaiting), ticket(3, StatusWaiting),
		},
		InProgress:  []sqlc.QueueTicket{ticket(0, StatusServing)},
		ServiceTime: ServiceTime{Average: 10 * time.Minute, Samples: MinServiceSamples},
	}

	// #3 has two people ahead and one counter working: two rounds of 10.
	if got := b.EstimateFor(3); got != 20*time.Minute {
		t.Fatalf("EstimateFor(#3) = %v, want 20m", got)
	}
	if got := b.EstimateFor(1); got != 0 {
		t.Fatalf("EstimateFor(#1) = %v, want no wait at the front of the line", got)
	}
}

func TestSecondsToDuration(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		want    time.Duration
	}{
		{"a plain average", 90, 90 * time.Second},
		{"a fractional average", 90.5, 90500 * time.Millisecond},
		{"an empty queue's zero", 0, 0},
		{"a negative value is not trusted", -5, 0},
		{"NaN is not trusted", math.NaN(), 0},
		{"infinity is not trusted", math.Inf(1), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secondsToDuration(tt.seconds); got != tt.want {
				t.Fatalf("secondsToDuration(%v) = %v, want %v", tt.seconds, got, tt.want)
			}
		})
	}
}
