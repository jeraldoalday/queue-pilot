package queue

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/db/sqlc"
)

// reportDay is the service day every ticket below belongs to, and the
// timezone is deliberately not UTC: the histogram buckets by the
// business's own hours, and a test run in UTC would not notice if that
// stopped being true.
var reportLoc = time.FixedZone("Test/+05:30", 5*3600+30*60)

func reportDay() time.Time {
	return time.Date(2026, time.March, 3, 0, 0, 0, 0, reportLoc)
}

// hhmm builds an instant on the report day at the given local hour and
// minute, which is how a person describes when something happened.
func hhmm(hour, minute int) pgtype.Timestamptz {
	d := reportDay()
	return db.Timestamptz(time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, reportLoc))
}

func nullTime() pgtype.Timestamptz { return pgtype.Timestamptz{} }

// ticket is a builder for the only fields the report reads.
func reportTicket(status Status, created, called, serving, completed pgtype.Timestamptz) sqlc.QueueTicket {
	return sqlc.QueueTicket{
		Status:      string(status),
		CreatedAt:   created,
		CalledAt:    called,
		ServingAt:   serving,
		CompletedAt: completed,
	}
}

func buildReport(tickets ...sqlc.QueueTicket) Report {
	return BuildReport(sqlc.Queue{Name: "Walk-ins"}, reportDay(), reportLoc, tickets)
}

func TestBuildReportCountsEveryStatus(t *testing.T) {
	r := buildReport(
		reportTicket(StatusWaiting, hhmm(9, 0), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusCalled, hhmm(9, 5), hhmm(9, 20), nullTime(), nullTime()),
		reportTicket(StatusServing, hhmm(9, 10), hhmm(9, 25), hhmm(9, 26), nullTime()),
		reportTicket(StatusDone, hhmm(9, 15), hhmm(9, 30), hhmm(9, 31), hhmm(9, 41)),
		reportTicket(StatusNoShow, hhmm(9, 20), hhmm(9, 35), nullTime(), hhmm(9, 40)),
		reportTicket(StatusCancelled, hhmm(9, 25), nullTime(), nullTime(), hhmm(9, 30)),
	)

	want := Stats{Waiting: 1, Called: 1, Serving: 1, Done: 1, NoShow: 1, Cancelled: 1}
	if r.Stats != want {
		t.Errorf("Stats = %+v, want %+v", r.Stats, want)
	}
	if r.Issued() != 6 {
		t.Errorf("Issued() = %d, want 6", r.Issued())
	}
	// Resolved counts outcomes, not tickets: three are still in play.
	if r.Resolved() != 3 {
		t.Errorf("Resolved() = %d, want 3 (done, no-show, cancelled)", r.Resolved())
	}
}

// The wait is what a customer would call the wait: issued until called.
func TestBuildReportMeasuresWaits(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 0), hhmm(9, 10), hhmm(9, 11), hhmm(9, 20)),  // 10m wait
		reportTicket(StatusDone, hhmm(9, 5), hhmm(9, 35), hhmm(9, 36), hhmm(9, 50)),  // 30m wait
		reportTicket(StatusNoShow, hhmm(9, 6), hhmm(9, 26), nullTime(), hhmm(9, 30)), // 20m wait
	)

	if r.Wait.Count != 3 {
		t.Fatalf("Wait.Count = %d, want 3", r.Wait.Count)
	}
	if got, want := r.Wait.Mean(), 20*time.Minute; got != want {
		t.Errorf("Wait.Mean() = %v, want %v", got, want)
	}
	if got, want := r.Wait.Min, 10*time.Minute; got != want {
		t.Errorf("Wait.Min = %v, want %v", got, want)
	}
	if got, want := r.Wait.Max, 30*time.Minute; got != want {
		t.Errorf("Wait.Max = %v, want %v", got, want)
	}
}

// A ticket that was never called has no wait to measure. Counting it as a
// zero-minute wait would make a day of abandoned tickets look fast.
func TestBuildReportSkipsUnmeasurableWaits(t *testing.T) {
	r := buildReport(
		reportTicket(StatusWaiting, hhmm(9, 0), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusCancelled, hhmm(9, 1), nullTime(), nullTime(), hhmm(9, 15)),
		reportTicket(StatusDone, hhmm(9, 2), hhmm(9, 22), hhmm(9, 23), hhmm(9, 30)),
	)

	if r.Wait.Count != 1 {
		t.Fatalf("Wait.Count = %d, want 1 — only the called ticket has a wait", r.Wait.Count)
	}
	if got, want := r.Wait.Mean(), 20*time.Minute; got != want {
		t.Errorf("Wait.Mean() = %v, want %v", got, want)
	}
}

// Hands-on time counts completed services only, matching the live wait
// estimate: a ticket abandoned mid-service says nothing about how long
// serving someone takes.
func TestBuildReportMeasuresCompletedServicesOnly(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 0), hhmm(9, 5), hhmm(9, 6), hhmm(9, 16)),         // 10m service
		reportTicket(StatusDone, hhmm(9, 10), hhmm(9, 15), hhmm(9, 16), hhmm(9, 36)),      // 20m service
		reportTicket(StatusCancelled, hhmm(9, 20), hhmm(9, 25), hhmm(9, 26), hhmm(9, 56)), // walked out mid-service
		reportTicket(StatusServing, hhmm(9, 30), hhmm(9, 35), hhmm(9, 36), nullTime()),    // still at the counter
	)

	if r.Service.Count != 2 {
		t.Fatalf("Service.Count = %d, want 2", r.Service.Count)
	}
	if got, want := r.Service.Mean(), 15*time.Minute; got != want {
		t.Errorf("Service.Mean() = %v, want %v", got, want)
	}
}

// Timestamps that run backwards can only come from clock movement. A
// negative sample is dropped rather than clamped, because a zero-second
// wait folded into an average misreports the day.
func TestBuildReportDropsNegativeDurations(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 30), hhmm(9, 10), hhmm(9, 11), hhmm(9, 20)),
	)

	if !r.Wait.Empty() {
		t.Errorf("Wait has %d samples, want none — the wait ran backwards", r.Wait.Count)
	}
}

// An empty summary must read as "no data", never as a confident zero.
func TestDurationSummaryEmpty(t *testing.T) {
	var d DurationSummary
	if !d.Empty() {
		t.Error("a zero DurationSummary is not Empty()")
	}
	if d.Mean() != 0 {
		t.Errorf("Mean() on no samples = %v, want 0", d.Mean())
	}
}

func TestBuildReportBucketsByOrgLocalHour(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 5), hhmm(9, 10), hhmm(9, 11), hhmm(9, 20)),
		reportTicket(StatusDone, hhmm(9, 50), hhmm(10, 0), hhmm(10, 1), hhmm(10, 15)),
		reportTicket(StatusWaiting, hhmm(11, 30), nullTime(), nullTime(), nullTime()),
	)

	// 09:00 and 11:00 are active with a quiet 10:00 between them.
	if len(r.Hours) != 3 {
		t.Fatalf("Hours has %d buckets, want 3 (09, 10, 11)", len(r.Hours))
	}
	want := []HourBucket{
		{Hour: 9, Issued: 2, Served: 2},
		{Hour: 10, Issued: 0, Served: 0},
		{Hour: 11, Issued: 1, Served: 0},
	}
	for i, w := range want {
		if r.Hours[i] != w {
			t.Errorf("Hours[%d] = %+v, want %+v", i, r.Hours[i], w)
		}
	}
}

// The quiet hour in the middle of a day is a fact about the day. Dropping
// the row would draw a five-hour rush where there was a lunch break.
func TestBuildReportKeepsQuietHoursInTheMiddle(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 0), hhmm(9, 5), hhmm(9, 6), hhmm(9, 15)),
		reportTicket(StatusDone, hhmm(14, 0), hhmm(14, 5), hhmm(14, 6), hhmm(14, 15)),
	)

	if len(r.Hours) != 6 {
		t.Fatalf("Hours has %d buckets, want 6 (09 through 14 inclusive)", len(r.Hours))
	}
	for _, h := range r.Hours[1:5] {
		if h.Issued != 0 {
			t.Errorf("hour %d has %d tickets, want a quiet hour", h.Hour, h.Issued)
		}
	}
}

// Nothing before or after the day's tickets is invented — a queue that
// opened at 09:00 should not be shown eight empty hours from midnight.
func TestBuildReportDoesNotPadTheEdgesOfTheDay(t *testing.T) {
	r := buildReport(reportTicket(StatusDone, hhmm(13, 0), hhmm(13, 5), hhmm(13, 6), hhmm(13, 15)))

	if len(r.Hours) != 1 || r.Hours[0].Hour != 13 {
		t.Fatalf("Hours = %+v, want exactly the 13:00 bucket", r.Hours)
	}
}

func TestBuildReportPeakHour(t *testing.T) {
	r := buildReport(
		reportTicket(StatusWaiting, hhmm(9, 0), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(11, 0), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(11, 10), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(11, 20), nullTime(), nullTime(), nullTime()),
	)

	peak, ok := r.PeakHour()
	if !ok {
		t.Fatal("PeakHour reported no peak for a day with tickets")
	}
	if peak.Hour != 11 || peak.Issued != 3 {
		t.Errorf("PeakHour = %+v, want hour 11 with 3 tickets", peak)
	}
	if r.PeakIssued() != 3 {
		t.Errorf("PeakIssued() = %d, want 3", r.PeakIssued())
	}
}

// On a tie the earlier hour wins: the start of a rush is what staffing
// decisions hang on, not its tail.
func TestBuildReportPeakHourPrefersTheEarlierOnATie(t *testing.T) {
	r := buildReport(
		reportTicket(StatusWaiting, hhmm(9, 0), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(15, 0), nullTime(), nullTime(), nullTime()),
	)

	peak, ok := r.PeakHour()
	if !ok {
		t.Fatal("PeakHour reported no peak")
	}
	if peak.Hour != 9 {
		t.Errorf("PeakHour = %d, want the earlier hour 9", peak.Hour)
	}
}

func TestEmptyReport(t *testing.T) {
	r := buildReport()

	if !r.Empty() {
		t.Error("a day with no tickets is not Empty()")
	}
	if len(r.Hours) != 0 {
		t.Errorf("Hours = %+v, want none for an empty day", r.Hours)
	}
	if _, ok := r.PeakHour(); ok {
		t.Error("PeakHour found a peak on an empty day")
	}
	if _, ok := r.NoShowPercent(); ok {
		t.Error("NoShowPercent reported a rate with nothing resolved")
	}
	if _, ok := r.CompletionPercent(); ok {
		t.Error("CompletionPercent reported a rate with nothing resolved")
	}
}

// Rates are over resolved tickets, not issued ones. A queue mid-morning
// has open tickets whose outcome nobody knows yet, and counting them as
// "not a no-show" would flatter the number until the day ended.
func TestReportRatesUseResolvedTicketsAsTheDenominator(t *testing.T) {
	r := buildReport(
		reportTicket(StatusDone, hhmm(9, 0), hhmm(9, 5), hhmm(9, 6), hhmm(9, 15)),
		reportTicket(StatusDone, hhmm(9, 1), hhmm(9, 6), hhmm(9, 7), hhmm(9, 16)),
		reportTicket(StatusDone, hhmm(9, 2), hhmm(9, 7), hhmm(9, 8), hhmm(9, 17)),
		reportTicket(StatusNoShow, hhmm(9, 3), hhmm(9, 8), nullTime(), hhmm(9, 18)),
		// Four tickets still open: if these counted, the rate would be 10%.
		reportTicket(StatusWaiting, hhmm(9, 4), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(9, 5), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(9, 6), nullTime(), nullTime(), nullTime()),
		reportTicket(StatusWaiting, hhmm(9, 7), nullTime(), nullTime(), nullTime()),
	)

	got, ok := r.NoShowPercent()
	if !ok {
		t.Fatal("NoShowPercent reported no rate")
	}
	if got != 25 {
		t.Errorf("NoShowPercent = %d%%, want 25%% (1 of 4 resolved)", got)
	}

	done, ok := r.CompletionPercent()
	if !ok {
		t.Fatal("CompletionPercent reported no rate")
	}
	if done != 75 {
		t.Errorf("CompletionPercent = %d%%, want 75%%", done)
	}
}

func TestNoShowPercentRounds(t *testing.T) {
	// 1 of 3 resolved is 33.3%, which must not truncate to 33 by accident
	// of integer division elsewhere; and 2 of 3 must round up to 67.
	one := buildReport(
		reportTicket(StatusNoShow, hhmm(9, 0), hhmm(9, 5), nullTime(), hhmm(9, 10)),
		reportTicket(StatusDone, hhmm(9, 1), hhmm(9, 6), hhmm(9, 7), hhmm(9, 11)),
		reportTicket(StatusDone, hhmm(9, 2), hhmm(9, 7), hhmm(9, 8), hhmm(9, 12)),
	)
	if got, _ := one.NoShowPercent(); got != 33 {
		t.Errorf("1 of 3 = %d%%, want 33%%", got)
	}

	two := buildReport(
		reportTicket(StatusNoShow, hhmm(9, 0), hhmm(9, 5), nullTime(), hhmm(9, 10)),
		reportTicket(StatusNoShow, hhmm(9, 1), hhmm(9, 6), nullTime(), hhmm(9, 11)),
		reportTicket(StatusDone, hhmm(9, 2), hhmm(9, 7), hhmm(9, 8), hhmm(9, 12)),
	)
	if got, _ := two.NoShowPercent(); got != 67 {
		t.Errorf("2 of 3 = %d%%, want 67%%", got)
	}
}

func TestHourBucketShare(t *testing.T) {
	tests := []struct {
		name   string
		bucket HourBucket
		peak   int
		want   int
	}{
		{"the peak itself is full width", HourBucket{Issued: 10}, 10, 100},
		{"half the peak", HourBucket{Issued: 5}, 10, 50},
		{"a quiet hour stays visible", HourBucket{Issued: 1}, 100, 3},
		{"an empty hour draws nothing", HourBucket{Issued: 0}, 10, 0},
		{"no peak draws nothing", HourBucket{Issued: 3}, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.bucket.Share(tt.peak); got != tt.want {
				t.Errorf("Share(%d) on %d tickets = %d, want %d", tt.peak, tt.bucket.Issued, got, tt.want)
			}
		})
	}
}

func TestHourBucketLabel(t *testing.T) {
	tests := []struct {
		hour int
		want string
	}{
		{0, "00:00–01:00"},
		{9, "09:00–10:00"},
		{13, "13:00–14:00"},
		{23, "23:00–00:00"}, // the last hour wraps rather than reading 24:00
	}

	for _, tt := range tests {
		if got := (HourBucket{Hour: tt.hour}).Label(); got != tt.want {
			t.Errorf("HourBucket{%d}.Label() = %q, want %q", tt.hour, got, tt.want)
		}
	}
}

// The report addresses the business reading its own history, so unlike
// FormatWait it does not round away the difference between six and nine
// minutes.
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "—"},
		{-time.Minute, "—"},
		{45 * time.Second, "45s"},
		{time.Minute, "1m 00s"},
		{6*time.Minute + 30*time.Second, "6m 30s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h 00m"},
		{2*time.Hour + 7*time.Minute, "2h 07m"},
	}

	for _, tt := range tests {
		if got := FormatDuration(tt.in); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A nil location must not panic: org.Location falls back to UTC on an
// unparseable zone, and a caller passing nil should degrade the same way
// rather than take the page down.
func TestBuildReportToleratesNilLocation(t *testing.T) {
	r := BuildReport(sqlc.Queue{}, reportDay(), nil, []sqlc.QueueTicket{
		reportTicket(StatusDone, hhmm(9, 0), hhmm(9, 5), hhmm(9, 6), hhmm(9, 15)),
	})
	if r.Issued() != 1 {
		t.Fatalf("Issued() = %d, want 1", r.Issued())
	}
}

// The ?date= parameter is navigation, not an identifier: a hand-edited or
// truncated value should land someone on the report they most likely
// wanted rather than on an error they have to back out of.
func TestParseDay(t *testing.T) {
	loc := reportLoc
	todayInLoc := today(loc)

	tests := []struct {
		name  string
		value string
		want  time.Time
	}{
		{"a date is taken as given", "2026-03-03", reportDay()},
		{"empty falls back to today", "", todayInLoc},
		{"nonsense falls back to today", "not-a-date", todayInLoc},
		{"a wrong format falls back to today", "03/03/2026", todayInLoc},
		{"an out-of-range date falls back to today", "2026-13-45", todayInLoc},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDay(tt.value, loc)
			if !got.Equal(tt.want) {
				t.Errorf("parseDay(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// A day parsed out of a URL must be midnight in the org's own timezone,
// or the report would read a neighbouring day's tickets for any business
// far enough from UTC.
func TestParseDayIsMidnightInTheOrgTimezone(t *testing.T) {
	got := parseDay("2026-03-03", reportLoc)

	if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("parseDay returned %v, want midnight", got)
	}
	if got.Location().String() != reportLoc.String() {
		t.Errorf("parseDay returned location %v, want %v", got.Location(), reportLoc)
	}
}

// "Next day" stops at today: a service day that has not happened yet has
// no tickets to report on, matching the "Jump to" date field's own max.
func TestHasNextReportDay(t *testing.T) {
	loc := reportLoc
	todayInLoc := today(loc)

	tests := []struct {
		name string
		day  time.Time
		want bool
	}{
		{"a day before today has a next day", todayInLoc.AddDate(0, 0, -1), true},
		{"a day well in the past has a next day", todayInLoc.AddDate(0, 0, -30), true},
		{"today itself has no next day", todayInLoc, false},
		{"a day already past today has no next day", todayInLoc.AddDate(0, 0, 1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasNextReportDay(tt.day, todayInLoc); got != tt.want {
				t.Errorf("hasNextReportDay(%v, %v) = %v, want %v", tt.day, todayInLoc, got, tt.want)
			}
		})
	}
}

func TestReportURL(t *testing.T) {
	const queueID = "0d6f4c1e-2b3a-4d5e-8f90-112233445566"

	if got, want := reportURL(queueID, "2026-03-03"), "/app/queues/"+queueID+"/report?date=2026-03-03"; got != want {
		t.Errorf("reportURL with a date = %q, want %q", got, want)
	}
	// No date means "today", and an empty query parameter would be a
	// different URL for the same page — worse for caching and for sharing.
	if got, want := reportURL(queueID, ""), "/app/queues/"+queueID+"/report"; got != want {
		t.Errorf("reportURL without a date = %q, want %q", got, want)
	}
}

// The bar is an inline width because the value is data; Tailwind never
// sees a runtime-computed class. It still must not be able to emit a
// width outside 0-100%.
func TestBarWidthIsClamped(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "width:0%"},
		{62, "width:62%"},
		{100, "width:100%"},
		{-5, "width:0%"},
		{140, "width:100%"},
	}

	for _, tt := range tests {
		if got := barWidth(tt.in); got != tt.want {
			t.Errorf("barWidth(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "ticket", "tickets"); got != "ticket" {
		t.Errorf("plural(1) = %q, want %q", got, "ticket")
	}
	for _, n := range []int{0, 2, 17} {
		if got := plural(n, "ticket", "tickets"); got != "tickets" {
			t.Errorf("plural(%d) = %q, want %q", n, got, "tickets")
		}
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("front-desk-2026-03-03.csv")
	want := `attachment; filename="front-desk-2026-03-03.csv"`
	if got != want {
		t.Errorf("contentDisposition(...) = %q, want %q", got, want)
	}
}

func TestContentDispositionMatchesExportFilename(t *testing.T) {
	got := contentDisposition(ExportFilename("front-desk", reportDay()))
	want := `attachment; filename="front-desk-2026-03-03.csv"`
	if got != want {
		t.Errorf("contentDisposition(ExportFilename(...)) = %q, want %q", got, want)
	}
}
