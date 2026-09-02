package metrics

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func render(r *Registry, extra ...Metric) string {
	var sb strings.Builder
	r.Write(&sb, "queuepilot", extra...)
	return sb.String()
}

func lineFor(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line starting %q in:\n%s", prefix, out)
	return ""
}

func TestObserveCountsByRouteMethodAndStatus(t *testing.T) {
	r := New()
	r.Observe("/q/:org/:queue", "GET", 200, 12*time.Millisecond)
	r.Observe("/q/:org/:queue", "GET", 200, 30*time.Millisecond)
	r.Observe("/q/:org/:queue", "GET", 404, 3*time.Millisecond)
	r.Observe("/book/:org", "GET", 200, 8*time.Millisecond)

	out := render(r)

	if got := lineFor(t, out, `queuepilot_http_requests_total{route="/q/:org/:queue",method="GET",status="200"}`); !strings.HasSuffix(got, " 2") {
		t.Errorf("two 200s were counted as %q", got)
	}
	if got := lineFor(t, out, `queuepilot_http_requests_total{route="/q/:org/:queue",method="GET",status="404"}`); !strings.HasSuffix(got, " 1") {
		t.Errorf("the 404 was counted as %q", got)
	}
	// A status is a label, not a separate metric: an error rate is
	// errors over requests, and both have to be on the same series.
	if r.Series() != 3 {
		t.Errorf("Series() = %d, want 3", r.Series())
	}
}

// Each bucket counts everything at or below its bound. Getting this wrong
// produces a histogram that renders as nonsense at every quantile, and it
// is the single easiest part of the format to get wrong.
func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := New()
	for _, d := range []time.Duration{3 * time.Millisecond, 30 * time.Millisecond, 400 * time.Millisecond} {
		r.Observe("/app", "GET", 200, d)
	}

	out := render(r)
	counts := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "queuepilot_http_request_duration_seconds_bucket") {
			continue
		}
		le := line[strings.Index(line, `le="`)+4:]
		le = le[:strings.Index(le, `"`)]
		counts[le] = line[strings.LastIndex(line, " ")+1:]
	}

	for le, want := range map[string]string{
		"0.005": "1", // the 3ms one
		"0.05":  "2", // and the 30ms one
		"0.5":   "3", // and the 400ms one
		"10":    "3",
		"+Inf":  "3",
	} {
		if counts[le] != want {
			t.Errorf("bucket le=%q = %q, want %q\n%s", le, counts[le], want, out)
		}
	}

	if got := lineFor(t, out, "queuepilot_http_request_duration_seconds_count"); !strings.HasSuffix(got, " 3") {
		t.Errorf("_count = %q, want 3", got)
	}
	if got := lineFor(t, out, "queuepilot_http_request_duration_seconds_sum"); !strings.HasSuffix(got, " 0.433") {
		t.Errorf("_sum = %q, want 0.433", got)
	}
}

// A request slower than every bound still has to be counted, or the
// histogram's own total disagrees with the request counter beside it.
func TestSlowRequestLandsInTheInfiniteBucket(t *testing.T) {
	r := New()
	r.Observe("/app", "GET", 200, 45*time.Second)

	out := render(r)
	if got := lineFor(t, out, `queuepilot_http_request_duration_seconds_bucket{route="/app",method="GET",status="200",le="10"}`); !strings.HasSuffix(got, " 0") {
		t.Errorf("a 45s request fell inside le=10: %q", got)
	}
	if got := lineFor(t, out, `queuepilot_http_request_duration_seconds_bucket{route="/app",method="GET",status="200",le="+Inf"}`); !strings.HasSuffix(got, " 1") {
		t.Errorf("+Inf bucket = %q, want 1", got)
	}
}

// A label that can take unbounded values turns this endpoint into a
// memory leak driven by strangers. The backstop is what keeps a caller's
// mistake from becoming an outage.
func TestSeriesAreCappedRatherThanGrowingForever(t *testing.T) {
	r := New()
	for i := 0; i < maxSeries+50; i++ {
		r.Observe("/oops/"+strconv.Itoa(i), "GET", 200, time.Millisecond)
	}

	if got := r.Series(); got > maxSeries+1 {
		t.Errorf("Series() = %d, want no more than %d", got, maxSeries+1)
	}

	// The requests past the cap are still counted, just not attributed.
	out := render(r)
	overflow := lineFor(t, out, `queuepilot_http_requests_total{route="overflow",method="GET",status="200"}`)
	if !strings.HasSuffix(overflow, " 50") {
		t.Errorf("overflow series = %q, want the 50 requests past the cap", overflow)
	}
}

func TestOutputIsStableAcrossRenders(t *testing.T) {
	r := New()
	for _, route := range []string{"/q/:org", "/book/:org", "/app", "/healthz"} {
		r.Observe(route, "GET", 200, time.Millisecond)
	}

	if render(r) != render(r) {
		t.Error("two renders of an unchanged registry differ — the endpoint cannot be diffed by eye")
	}
}

func TestExtraMetricsAreWrittenFirst(t *testing.T) {
	r := New()
	r.Observe("/app", "GET", 200, time.Millisecond)

	out := render(r,
		Metric{Name: "build_info", Help: "The build this process is running.", Type: "gauge", Value: 1,
			Labels: []Label{{Name: "revision", Value: "abc123"}}},
		Metric{Name: "uptime_seconds", Help: "Seconds since start.", Type: "gauge", Value: 12.5},
	)

	if got := lineFor(t, out, "queuepilot_build_info{"); got != `queuepilot_build_info{revision="abc123"} 1` {
		t.Errorf("build_info = %q", got)
	}
	if got := lineFor(t, out, "queuepilot_uptime_seconds "); got != "queuepilot_uptime_seconds 12.5" {
		t.Errorf("uptime = %q", got)
	}
	if strings.Index(out, "queuepilot_build_info") > strings.Index(out, "queuepilot_http_requests_total") {
		t.Error("the build line is below the traffic it explains")
	}
}

// A label value that breaks the format takes the whole scrape with it,
// not one line. Build information comes from the build rather than from
// this file, so it is escaped rather than trusted.
func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	out := render(r, Metric{Name: "build_info", Help: "h", Type: "gauge", Value: 1,
		Labels: []Label{{Name: "revision", Value: "a\"b\\c\nd"}}})

	if got := lineFor(t, out, "queuepilot_build_info{"); got != `queuepilot_build_info{revision="a\"b\\c\nd"} 1` {
		t.Errorf("escaped label = %q", got)
	}
}

// Numbers are read by people as well as by scrapers, and an exponent is
// harder to read at a glance than a decimal.
func TestFloatsAreWrittenWithoutExponents(t *testing.T) {
	if got := formatFloat(0.000015); got != "0.000015" {
		t.Errorf("formatFloat(0.000015) = %q", got)
	}
	if got := formatFloat(1); got != "1" {
		t.Errorf("formatFloat(1) = %q, want no trailing zeroes", got)
	}
}

// The registry is written by every request at once and read by a scrape
// in the middle of them. Run with -race, this is the test that says so.
func TestConcurrentObservationsAndScrapes(t *testing.T) {
	r := New()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Observe("/q/:org/:queue", "GET", 200, time.Duration(j)*time.Millisecond)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			_ = render(r)
		}
	}()
	wg.Wait()

	if got := lineFor(t, render(r), `queuepilot_http_requests_total{route="/q/:org/:queue",method="GET",status="200"}`); !strings.HasSuffix(got, " 1600") {
		t.Errorf("counter after 1600 concurrent observations = %q", got)
	}
}

// A nil registry is what a caller that has not enabled metrics holds, and
// recording into one must be a no-op rather than a panic in every handler.
func TestNilRegistryIsSafe(t *testing.T) {
	var r *Registry
	r.Observe("/app", "GET", 200, time.Millisecond)
}
