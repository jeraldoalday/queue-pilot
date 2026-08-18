package queue

import "fmt"

// dateLayout is how a service day is written in a URL and in the report's
// date field. It is ISO order so that sorting a list of them sorts by
// date, and so the value is what <input type="date"> both emits and reads.
const dateLayout = "2006-01-02"

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
