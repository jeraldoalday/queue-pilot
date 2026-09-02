package queue

import (
	"testing"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

func line(name string, waiting int, wait time.Duration) Line {
	return Line{
		Queue:   sqlc.Queue{Name: name, Slug: name},
		Waiting: waiting,
		Wait:    wait,
	}
}

// The top of this list is advice: a customer scanning it reads the first
// entry as "join this one". Ordering by anything but the wait would be
// advice about something the customer did not ask.
func TestSortLinesPutsTheShortestWaitFirst(t *testing.T) {
	lines := []Line{
		line("Prescriptions", 2, 20*time.Minute),
		line("General", 6, 5*time.Minute),
		line("Returns", 1, 45*time.Minute),
	}

	SortLines(lines)

	want := []string{"General", "Prescriptions", "Returns"}
	for i, name := range want {
		if lines[i].Queue.Name != name {
			t.Fatalf("order = %s, want %v", names(lines), want)
		}
	}
}

// Six people in front of three counters clear faster than two in front of
// one, and the wait is what the person in the doorway is choosing on.
func TestSortLinesPrefersTheShorterWaitOverTheShorterLine(t *testing.T) {
	lines := []Line{
		line("Two people, one counter", 2, 30*time.Minute),
		line("Six people, three counters", 6, 10*time.Minute),
	}

	SortLines(lines)

	if lines[0].Queue.Name != "Six people, three counters" {
		t.Errorf("order = %s, want the longer line with the shorter wait first", names(lines))
	}
}

// A tie must not reorder itself between one customer's page and the next:
// two people reading the same screen should be told the same thing.
func TestSortLinesBreaksTiesByName(t *testing.T) {
	lines := []Line{
		line("Returns", 3, 15*time.Minute),
		line("General", 3, 15*time.Minute),
	}
	SortLines(lines)
	first := names(lines)

	SortLines(lines)
	if names(lines) != first {
		t.Errorf("sorting twice changed the order: %s then %s", first, names(lines))
	}
	if lines[0].Queue.Name != "General" {
		t.Errorf("order = %s, want the tie broken by name", names(lines))
	}
}

func TestTotalWaitingAndBusiestLine(t *testing.T) {
	lines := []Line{
		line("General", 6, 5*time.Minute),
		line("Returns", 1, 45*time.Minute),
	}

	if got := TotalWaiting(lines); got != 7 {
		t.Errorf("TotalWaiting = %d, want 7", got)
	}

	busiest, ok := BusiestLine(lines)
	if !ok {
		t.Fatal("BusiestLine reported nothing for a floor with two lines")
	}
	// The busiest line is the longest one, not the slowest: it is an
	// operator's question ("where is the crowd"), not a customer's.
	if busiest.Queue.Name != "General" {
		t.Errorf("BusiestLine = %q, want General", busiest.Queue.Name)
	}

	if _, ok := BusiestLine(nil); ok {
		t.Error("BusiestLine invented a line for an org with none")
	}
	if got := TotalWaiting(nil); got != 0 {
		t.Errorf("TotalWaiting(nil) = %d, want 0", got)
	}
}

func TestNewLineFoldsABoardDown(t *testing.T) {
	board := Board{
		Queue:       sqlc.Queue{Name: "General", Slug: "general"},
		Stats:       Stats{Waiting: 4, Called: 1},
		ServiceTime: ServiceTime{Average: 10 * time.Minute, Samples: MinServiceSamples},
		InProgress:  []sqlc.QueueTicket{{Number: 12, Status: string(StatusCalled)}},
	}

	got := NewLine(board)

	if got.Waiting != 4 {
		t.Errorf("Waiting = %d, want 4", got.Waiting)
	}
	if !got.Serving || got.NowServing != 12 {
		t.Errorf("NowServing = (%d, %v), want (12, true)", got.NowServing, got.Serving)
	}
	if got.Wait != Estimate(4, board.ServiceTime, board.ActiveServers()) {
		t.Errorf("Wait = %v, want the board's own estimate", got.Wait)
	}
}

// A queue that has not called anybody yet is not serving number zero.
func TestNewLineOnAQueueThatHasNotStarted(t *testing.T) {
	got := NewLine(Board{Queue: sqlc.Queue{Name: "General"}})

	if got.Serving {
		t.Errorf("Serving = true with NowServing %d, want false", got.NowServing)
	}
}

func TestWaitingSummary(t *testing.T) {
	tests := []struct {
		name  string
		lines []Line
		want  string
	}{
		{"one empty line", []Line{line("General", 0, 0)}, "One line, nobody waiting."},
		{"two empty lines", []Line{line("General", 0, 0), line("Returns", 0, 0)}, "2 lines, nobody waiting."},
		{"one person", []Line{line("General", 1, 0)}, "One person waiting."},
		{"several people across lines", []Line{line("General", 4, 0), line("Returns", 3, 0)}, "7 people waiting."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := waitingSummary(tt.lines); got != tt.want {
				t.Errorf("waitingSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func names(lines []Line) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += ", "
		}
		out += l.Queue.Name
	}
	return out
}
