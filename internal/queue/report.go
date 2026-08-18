package queue

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// DurationSummary is the shape of one measured duration across a day's
// tickets. Mean alone hides the day a queue ran fine except for the one
// customer who waited an hour, so the extremes are carried alongside it.
type DurationSummary struct {
	Count int
	Total time.Duration
	Min   time.Duration
	Max   time.Duration
}

// Empty reports whether anything was measured. A summary with no samples
// must be rendered as "no data", never as a confident zero.
func (d DurationSummary) Empty() bool { return d.Count == 0 }

// Mean is the average, or zero when nothing was measured.
func (d DurationSummary) Mean() time.Duration {
	if d.Count == 0 {
		return 0
	}
	return d.Total / time.Duration(d.Count)
}

// add folds one more sample in.
func (d DurationSummary) add(v time.Duration) DurationSummary {
	if d.Count == 0 || v < d.Min {
		d.Min = v
	}
	if v > d.Max {
		d.Max = v
	}
	d.Count++
	d.Total += v
	return d
}

// HourBucket is one hour of the business's own day: how many tickets were
// taken in it and how many of those the queue got through.
type HourBucket struct {
	Hour   int // 0-23, in the org's timezone
	Issued int
	Served int
}

// Label renders the hour as a range, e.g. "09:00–10:00".
func (h HourBucket) Label() string {
	return fmt.Sprintf("%02d:00–%02d:00", h.Hour, (h.Hour+1)%24)
}

// Share is this bucket's issued count as a percentage of the busiest
// bucket in the same report, which is what a bar chart drawn in CSS needs.
// It is clamped to a visible minimum so an hour with one ticket is a
// sliver rather than nothing at all — an invisible bar reads as a missing
// hour rather than a quiet one.
func (h HourBucket) Share(peak int) int {
	if peak <= 0 || h.Issued <= 0 {
		return 0
	}
	pct := h.Issued * 100 / peak
	if pct < 3 {
		return 3
	}
	return pct
}

// Report is one queue's worked day, as the business would describe it:
// how many people it took, how long they waited, how long each took to
// serve, and when the rush was.
//
// It exists because the console answers "what is happening now" and
// nothing answered "how did yesterday go" — the question behind staffing
// a counter, choosing opening hours, or telling whether a change helped.
type Report struct {
	Queue sqlc.Queue
	Day   time.Time
	Stats Stats

	// Wait is how long tickets sat between being issued and being called:
	// the number a customer would recognise as "the wait".
	Wait DurationSummary
	// Service is hands-on time at the counter, the same measure the live
	// wait estimate is built from.
	Service DurationSummary

	// Hours covers the span from the first ticket of the day to the last,
	// including any quiet hours in between — a gap in the middle of the
	// day is a fact about the day, not a row to omit.
	Hours []HourBucket
}

// Issued is how many tickets the day produced.
func (r Report) Issued() int { return r.Stats.Issued() }

// Resolved is the tickets that reached an outcome, which is the
// denominator any rate should use: a queue still mid-morning has open
// tickets whose outcome nobody knows yet, and counting them as
// "not a no-show" would flatter the number until the day ended.
func (r Report) Resolved() int {
	return r.Stats.Done + r.Stats.NoShow + r.Stats.Cancelled
}

// NoShowPercent is the share of resolved tickets that were called and not
// answered, rounded to a whole percent. It returns ok=false when nothing
// has resolved yet, so the page can say so rather than print "0%".
func (r Report) NoShowPercent() (int, bool) {
	resolved := r.Resolved()
	if resolved == 0 {
		return 0, false
	}
	return int(float64(r.Stats.NoShow)/float64(resolved)*100 + 0.5), true
}

// CompletionPercent is the share of resolved tickets that were served
// through to done.
func (r Report) CompletionPercent() (int, bool) {
	resolved := r.Resolved()
	if resolved == 0 {
		return 0, false
	}
	return int(float64(r.Stats.Done)/float64(resolved)*100 + 0.5), true
}

// PeakIssued is the busiest hour's ticket count, the scale every bar in
// the histogram is drawn against.
func (r Report) PeakIssued() int {
	peak := 0
	for _, h := range r.Hours {
		if h.Issued > peak {
			peak = h.Issued
		}
	}
	return peak
}

// PeakHour is the busiest hour of the day. It returns ok=false for a day
// with no tickets, and on a tie takes the earliest hour — the start of a
// rush is more useful to staff for than its tail.
func (r Report) PeakHour() (HourBucket, bool) {
	var best HourBucket
	found := false
	for _, h := range r.Hours {
		if h.Issued > best.Issued {
			best, found = h, true
		}
	}
	return best, found
}

// Empty reports whether the day has no tickets at all, which the page
// renders as a sentence rather than as a screen of zeroes.
func (r Report) Empty() bool { return r.Issued() == 0 }

// BuildReport aggregates one day's tickets. It is pure — every input is a
// value — so the arithmetic that a business will read as "our no-show rate
// went up" can be tested exhaustively without a database.
//
// loc is the org's timezone, and it matters: the timestamps are absolute
// instants, and bucketing them by the server's hours would report a shop's
// lunchtime rush as happening at some other time of day.
func BuildReport(queue sqlc.Queue, day time.Time, loc *time.Location, tickets []sqlc.QueueTicket) Report {
	if loc == nil {
		loc = time.UTC
	}

	r := Report{Queue: queue, Day: day}
	byHour := make(map[int]*HourBucket)

	for _, t := range tickets {
		switch Status(t.Status) {
		case StatusWaiting:
			r.Stats.Waiting++
		case StatusCalled:
			r.Stats.Called++
		case StatusServing:
			r.Stats.Serving++
		case StatusDone:
			r.Stats.Done++
		case StatusNoShow:
			r.Stats.NoShow++
		case StatusCancelled:
			r.Stats.Cancelled++
		}

		hour := t.CreatedAt.Time.In(loc).Hour()
		b, ok := byHour[hour]
		if !ok {
			b = &HourBucket{Hour: hour}
			byHour[hour] = b
		}
		b.Issued++
		if Status(t.Status) == StatusDone {
			b.Served++
		}

		if d, ok := span(t.CreatedAt, t.CalledAt); ok {
			r.Wait = r.Wait.add(d)
		}
		// Only completed services count toward hands-on time, for the same
		// reason the live estimate ignores everything else: a ticket
		// cancelled mid-service says nothing about how long serving takes.
		if d, ok := span(t.ServingAt, t.CompletedAt); ok && Status(t.Status) == StatusDone {
			r.Service = r.Service.add(d)
		}
	}

	r.Hours = fillHours(byHour)
	return r
}

// span measures the interval between two timestamps, either of which may
// be NULL — a ticket cancelled while waiting was never called, and one
// still at the counter has not completed.
//
// A negative result is discarded rather than clamped to zero. It can only
// come from clock movement between two writes, and a zero-second wait
// folded into an average is a quiet lie about how the day went, whereas a
// dropped sample is merely one fewer.
func span(from, to pgtype.Timestamptz) (time.Duration, bool) {
	if !from.Valid || !to.Valid {
		return 0, false
	}
	d := to.Time.Sub(from.Time)
	if d < 0 {
		return 0, false
	}
	return d, true
}

// fillHours turns the sparse map into a contiguous run from the first
// active hour to the last. A queue that takes tickets at 09:00 and again
// at 14:00 had a quiet lunchtime, and a chart that omits those rows
// misrepresents it as a busy five-hour stretch.
func fillHours(byHour map[int]*HourBucket) []HourBucket {
	if len(byHour) == 0 {
		return nil
	}

	hours := make([]int, 0, len(byHour))
	for h := range byHour {
		hours = append(hours, h)
	}
	sort.Ints(hours)

	first, last := hours[0], hours[len(hours)-1]
	out := make([]HourBucket, 0, last-first+1)
	for h := first; h <= last; h++ {
		if b, ok := byHour[h]; ok {
			out = append(out, *b)
			continue
		}
		out = append(out, HourBucket{Hour: h})
	}
	return out
}

// LoadReport reads a queue's tickets for one service day and aggregates
// them. `day` must already be resolved in the org's timezone.
func LoadReport(ctx context.Context, pool *pgxpool.Pool, queue sqlc.Queue, day time.Time, loc *time.Location) (Report, error) {
	tickets, err := sqlc.New(pool).ListTicketsForQueueOnDate(ctx, sqlc.ListTicketsForQueueOnDateParams{
		QueueID:    queue.ID,
		TicketDate: db.Date(day),
	})
	if err != nil {
		return Report{}, err
	}
	return BuildReport(queue, day, loc, tickets), nil
}

// FormatDuration renders a measured duration for the report page.
//
// It is deliberately not FormatWait: that one addresses a customer and
// rounds hard, because a promise should not sound more precise than it is.
// This addresses the business reading its own history, where the
// difference between six and nine minutes is the point.
func FormatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
