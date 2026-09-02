package queue

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// The front door for a business's walk-in lines.
//
// A queue's board already answers "how long is this line", and that is
// the page a printed QR code points at. It is the wrong page for somebody
// standing in a doorway with two lines in front of them, because it
// assumes they have already chosen. This is the list they choose from,
// and it exists on the server-rendered side for the same reason every
// other public page does: it has to work from a QR code, on a borrowed
// phone, with scripting switched off.

// Line is one queue as it appears on a list of them: enough to choose
// between two lines, not enough to be a board.
//
// It deliberately carries no customer names or ticket IDs. This page is
// read by whoever walks in, so it holds itself to the waiting-room
// display's standard rather than the ticket page's.
type Line struct {
	Queue   sqlc.Queue
	Waiting int
	// Wait is what somebody joining now would expect to wait.
	Wait time.Duration
	// NowServing is the number at the counter, and Serving says whether
	// there is one — a queue that has not started is not serving number
	// zero.
	NowServing int32
	Serving    bool
}

// Shortest reports whether this line is the one to join, given another.
//
// The comparison is on the wait rather than on the number of people
// waiting: three customers in front of two counters is a shorter wait
// than two in front of one, and the wait is what the person in the
// doorway is actually choosing on.
func (l Line) Shortest(other Line) bool {
	if l.Wait != other.Wait {
		return l.Wait < other.Wait
	}
	// A genuine tie is broken by name, so the list does not reorder
	// itself between one customer's page and the next.
	return l.Queue.Name < other.Queue.Name
}

// SortLines orders a directory shortest wait first.
//
// This is the one place in the app where the order of a list is advice
// rather than presentation: the top of this list is what the page is
// recommending, and a customer scanning it reads the first entry as "join
// this one". Ordering by name instead would recommend whichever line was
// named earliest in the alphabet, which is advice about nothing.
func SortLines(lines []Line) {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Shortest(lines[j]) })
}

// BusiestLine returns the longest line, which is what an operator wants
// to see and what the dashboard already reports. It is here so the two
// answers come from one definition.
func BusiestLine(lines []Line) (Line, bool) {
	var busiest Line
	found := false
	for _, l := range lines {
		if !found || l.Waiting > busiest.Waiting {
			busiest, found = l, true
		}
	}
	return busiest, found
}

// TotalWaiting is how many people are in this org's lines altogether.
func TotalWaiting(lines []Line) int {
	total := 0
	for _, l := range lines {
		total += l.Waiting
	}
	return total
}

// LoadDirectory assembles every open line for one org on one service day.
//
// Only active queues are listed. A closed line still answers on its own
// URL — a display in a room should say "closed" rather than go blank —
// but offering one on a list of places to join is offering a locked door.
//
// `day` must already be resolved in the org's timezone, like every other
// service-day argument in this package.
func LoadDirectory(ctx context.Context, pool *pgxpool.Pool, orgID string, day time.Time) ([]Line, error) {
	queues, err := ListActive(ctx, pool, orgID)
	if err != nil {
		return nil, err
	}

	lines := make([]Line, 0, len(queues))
	for _, q := range queues {
		board, err := LoadBoard(ctx, pool, q, day)
		if err != nil {
			return nil, err
		}
		lines = append(lines, NewLine(board))
	}

	SortLines(lines)
	return lines, nil
}

// NewLine folds a loaded board down to the list view of it. It is separate
// from LoadDirectory so the shape of the list can be tested without a
// database, which is where every rule about what this page may show lives.
func NewLine(board Board) Line {
	line := Line{
		Queue:   board.Queue,
		Waiting: board.Stats.Waiting,
		Wait:    Estimate(board.Stats.Waiting, board.ServiceTime, board.ActiveServers()),
	}
	if ns, ok := board.NowServing(); ok {
		line.NowServing, line.Serving = ns.Number, true
	}
	return line
}

// HasBookableServices reports whether an org offers anything to book, so
// the walk-in page can point at the booking page only when there is one
// worth pointing at. A link to an empty list is worse than no link: it
// reads as "we take appointments" and then says otherwise.
func HasBookableServices(ctx context.Context, pool *pgxpool.Pool, orgID string) (bool, error) {
	services, err := sqlc.New(pool).ListBookableServicesByOrg(ctx, db.MustUUID(orgID))
	if err != nil {
		return false, err
	}
	return len(services) > 0, nil
}
