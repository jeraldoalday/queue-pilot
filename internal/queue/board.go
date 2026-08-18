package queue

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// Stats is a queue's ticket counts for one service day.
type Stats struct {
	Waiting   int
	Called    int
	Serving   int
	Done      int
	NoShow    int
	Cancelled int
}

// Issued is every ticket taken that day, whatever became of it.
func (s Stats) Issued() int {
	return s.Waiting + s.Called + s.Serving + s.Done + s.NoShow + s.Cancelled
}

// Open is the tickets still in play.
func (s Stats) Open() int {
	return s.Waiting + s.Called + s.Serving
}

// Board is everything one queue's display needs for one service day,
// assembled once so the staff console, the public board and the customer's
// ticket page all read the same numbers rather than each running their own
// slightly different queries.
type Board struct {
	Queue       sqlc.Queue
	Day         time.Time
	Waiting     []sqlc.QueueTicket
	InProgress  []sqlc.QueueTicket // called or being served, lowest number first
	Stats       Stats
	ServiceTime ServiceTime
}

// ActiveServers is how many counters appear to be working the line, used
// to divide the wait estimate. Each ticket currently called or being
// served stands for one occupied counter — an approximation, but one drawn
// from what the queue is actually doing rather than from a configured
// number nobody remembers to update.
//
// A line with nothing in progress still needs an answer, so the floor is 1.
func (b Board) ActiveServers() int {
	if n := len(b.InProgress); n > 0 {
		return n
	}
	return 1
}

// NowServing is the ticket to show in the large "now serving" position:
// the lowest-numbered one currently in progress. It returns ok=false when
// nothing is in progress, which the board renders as a dash rather than as
// a zero.
func (b Board) NowServing() (sqlc.QueueTicket, bool) {
	if len(b.InProgress) == 0 {
		return sqlc.QueueTicket{}, false
	}
	return b.InProgress[0], true
}

// AheadOf returns how many waiting tickets sit in front of `number`.
// Counting from the already-loaded waiting list keeps the board's numbers
// self-consistent: a customer's stated position always matches the list
// rendered beside it, even if a ticket is issued between two queries.
func (b Board) AheadOf(number int32) int {
	ahead := 0
	for _, t := range b.Waiting {
		if t.Number < number {
			ahead++
		}
	}
	return ahead
}

// EstimateFor is the wait a customer holding `number` can expect.
func (b Board) EstimateFor(number int32) time.Duration {
	return Estimate(b.AheadOf(number), b.ServiceTime, b.ActiveServers())
}

// LoadBoard assembles a queue's board for one service day. `day` must
// already be resolved in the org's timezone.
func LoadBoard(ctx context.Context, pool *pgxpool.Pool, queue sqlc.Queue, day time.Time) (Board, error) {
	q := sqlc.New(pool)

	open, err := q.ListOpenTicketsForQueueOnDate(ctx, sqlc.ListOpenTicketsForQueueOnDateParams{
		QueueID:    queue.ID,
		TicketDate: db.Date(day),
	})
	if err != nil {
		return Board{}, err
	}

	board := Board{Queue: queue, Day: day}
	for _, t := range open {
		if Status(t.Status) == StatusWaiting {
			board.Waiting = append(board.Waiting, t)
		} else {
			board.InProgress = append(board.InProgress, t)
		}
	}

	stats, err := q.QueueStatsForDate(ctx, sqlc.QueueStatsForDateParams{
		QueueID:    queue.ID,
		TicketDate: db.Date(day),
	})
	if err != nil {
		return Board{}, err
	}
	board.Stats = Stats{
		Waiting:   int(stats.Waiting),
		Called:    int(stats.Called),
		Serving:   int(stats.Serving),
		Done:      int(stats.Done),
		NoShow:    int(stats.NoShow),
		Cancelled: int(stats.Cancelled),
	}

	st, err := ServiceTimeFor(ctx, pool, queue)
	if err != nil {
		return Board{}, err
	}
	board.ServiceTime = st

	return board, nil
}

// ServiceTimeFor reads a queue's measured service time. It looks across
// recent days rather than only today: at 09:05 a queue has no completed
// tickets yet, and the estimate customers see first would otherwise always
// be the fallback.
func ServiceTimeFor(ctx context.Context, pool *pgxpool.Pool, queue sqlc.Queue) (ServiceTime, error) {
	row, err := sqlc.New(pool).AverageServiceSeconds(ctx, queue.ID)
	if err != nil {
		return ServiceTime{}, err
	}
	return ServiceTime{
		Average: secondsToDuration(row.AvgSeconds),
		Samples: int(row.SampleCount),
	}, nil
}

// secondsToDuration converts the average Postgres returns as a float into
// a Duration. A negative or non-finite value (impossible from the query,
// but not worth trusting into a customer-facing number) becomes zero,
// which ServiceTime.Measured then treats as "no data".
func secondsToDuration(seconds float64) time.Duration {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
