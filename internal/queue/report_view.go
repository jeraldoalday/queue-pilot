package queue

import (
	"fmt"
	"time"
)

// dateLayout is how a service day is written in a URL and in the report's
// date field. It is ISO order so that sorting a list of them sorts by
// date, and so the value is what <input type="date"> both emits and reads.
const dateLayout = "2006-01-02"

// hasNextReportDay reports whether the "Next day" link should be offered
// from a report currently showing `day`.
//
// Forward navigation stops at today: a service day that has not happened
// yet has no tickets to report on, so the day after today is never worth
// linking to. The "Jump to" date field already enforces this with its own
// max — this is the same rule applied to the other way of moving forward,
// so a business clicking through one day at a time cannot page past today
// into a run of empty reports.
func hasNextReportDay(day, today time.Time) bool {
	return day.Before(today)
}

// barWidth is the inline style for one histogram bar.
//
// Tailwind cannot help here: the width is data, and a class name computed
// at runtime is a class name the CSS build never saw, so it would render
// as no width at all. An inline style is the honest way to say "this bar
// is 62% of the widest one".
func barWidth(percent int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("width:%d%%", percent)
}

// plural picks the wording for a count. It exists so the templates read
// as sentences rather than as "3 ticket(s)".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
