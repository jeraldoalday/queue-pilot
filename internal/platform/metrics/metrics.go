// Package metrics counts what the server did, in the text format
// Prometheus and every tool that speaks it can read.
//
// It exists because the access log answers "what happened to this
// request" and nothing else. The questions an operator actually has —
// is the error rate climbing, did the last deploy make the booking page
// slower, is one endpoint taking all the traffic — are questions about
// thousands of requests at once, and the only way to answer them from a
// log is to ship the log somewhere that can aggregate it.
//
// Deliberately small: counters and a fixed-bucket histogram, held in
// memory, with no background goroutine and no dependency. That has one
// real consequence, the same one the rate limiter has: the numbers are
// per process, so N instances are N series to add up rather than one. For
// a walk-in queue that is the right trade.
//
// The cardinality rule is the thing to be careful about. A time series is
// created per distinct label combination, so a label that can take
// unbounded values — a path with an org slug or a ticket ID in it — turns
// a metrics endpoint into an unbounded memory leak driven by strangers.
// Callers pass a route *template*, never a path, and Registry refuses to
// track more than maxSeries of them.
package metrics

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxSeries bounds how many distinct label combinations are tracked.
//
// It is a backstop, not a budget: the route labels are templates from a
// closed set, so a healthy process settles well under this. Hitting it
// means a caller is passing raw paths, and the failure it prevents —
// memory growing forever because somebody is fetching random URLs — is
// worse than the one it causes, which is a few requests counted only in
// the overflow series.
const maxSeries = 1000

// overflowRoute is where observations land once maxSeries is reached. The
// requests are still counted; what is lost is which route they were for.
const overflowRoute = "overflow"

// buckets are the histogram's upper bounds, in seconds.
//
// They are chosen for what this app does rather than copied from a
// default: nearly every response is a template render over one or two
// queries, which is single-digit milliseconds locally and tens of
// milliseconds against a hosted database, so the resolution is where the
// answers are. The last bucket matters as much as the first — a request
// over ten seconds is one a customer has already given up on.
var buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type key struct {
	route  string
	method string
	status int
}

type series struct {
	count uint64
	// nanos is the total time, accumulated as an integer rather than as
	// a float. Adding milliseconds as float64 accumulates error, and the
	// visible result is a _sum of 0.43300000000000005 on an endpoint that
	// is read by people as well as by scrapers.
	nanos int64
	// counts holds one bucket per entry in buckets, plus a final entry
	// for everything above the last bound — the +Inf bucket, which a
	// histogram is required to expose and which is where a request that
	// took a minute is recorded.
	counts []uint64
}

// Registry accumulates the counts. Its zero value is not usable; call New.
type Registry struct {
	mu     sync.Mutex
	series map[key]*series
}

func New() *Registry {
	return &Registry{series: make(map[key]*series)}
}

// Observe records one finished request.
//
// route must be a template from a closed set ("/q/:org/:queue"), never a
// path: see the package comment on cardinality.
func (r *Registry) Observe(route, method string, status int, d time.Duration) {
	if r == nil {
		return
	}

	k := key{route: route, method: method, status: status}

	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.series[k]
	if !ok {
		if len(r.series) >= maxSeries {
			k = key{route: overflowRoute, method: method, status: status}
			s, ok = r.series[k]
		}
		if !ok {
			s = &series{counts: make([]uint64, len(buckets)+1)}
			r.series[k] = s
		}
	}

	s.count++
	// A negative duration is impossible from a monotonic clock, but a sum
	// that can go backwards makes a rate() nonsensical, so it is floored.
	d = max(d, 0)
	s.nanos += d.Nanoseconds()
	s.counts[bucketFor(d.Seconds())]++
}

// bucketFor returns the index of the first bucket a duration falls in, or
// the +Inf bucket when it exceeds every bound.
func bucketFor(seconds float64) int {
	for i, bound := range buckets {
		if seconds <= bound {
			return i
		}
	}
	return len(buckets)
}

// Series reports how many distinct label combinations are being tracked.
// It exists for tests and for an operator wondering whether the
// cardinality backstop is anywhere near being hit.
func (r *Registry) Series() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.series)
}

// Write renders the registry in the Prometheus text exposition format.
//
// The output is sorted. Nothing consuming it requires that, but a metrics
// endpoint is also read by people, and a page whose lines move on every
// scrape is one you cannot diff against the last time you looked.
//
// Histogram buckets are cumulative — each le= bucket counts everything at
// or below its bound — which is what the format requires and the single
// easiest thing to get wrong.
func (r *Registry) Write(sb *strings.Builder, namespace string, extra ...Metric) {
	r.mu.Lock()
	keys := make([]key, 0, len(r.series))
	snapshot := make(map[key]series, len(r.series))
	for k, s := range r.series {
		keys = append(keys, k)
		copied := series{count: s.count, nanos: s.nanos, counts: make([]uint64, len(s.counts))}
		copy(copied.counts, s.counts)
		snapshot[k] = copied
	}
	r.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	for _, m := range extra {
		m.write(sb, namespace)
	}

	requests := namespace + "_http_requests_total"
	sb.WriteString("# HELP " + requests + " Requests handled, by route template, method and status.\n")
	sb.WriteString("# TYPE " + requests + " counter\n")
	for _, k := range keys {
		sb.WriteString(requests + labels(k) + " " + strconv.FormatUint(snapshot[k].count, 10) + "\n")
	}

	duration := namespace + "_http_request_duration_seconds"
	sb.WriteString("# HELP " + duration + " How long handling took, in seconds.\n")
	sb.WriteString("# TYPE " + duration + " histogram\n")
	for _, k := range keys {
		s := snapshot[k]
		cumulative := uint64(0)
		for i, bound := range buckets {
			cumulative += s.counts[i]
			sb.WriteString(duration + "_bucket" + labelsWith(k, "le", formatFloat(bound)) +
				" " + strconv.FormatUint(cumulative, 10) + "\n")
		}
		cumulative += s.counts[len(buckets)]
		sb.WriteString(duration + `_bucket` + labelsWith(k, "le", "+Inf") +
			" " + strconv.FormatUint(cumulative, 10) + "\n")
		sb.WriteString(duration + "_sum" + labels(k) + " " + formatFloat(float64(s.nanos)/float64(time.Second)) + "\n")
		sb.WriteString(duration + "_count" + labels(k) + " " + strconv.FormatUint(s.count, 10) + "\n")
	}
}

// Metric is a single value written alongside the request series — an
// uptime, a build identifier, anything an operator wants on the same page
// as the traffic it explains.
type Metric struct {
	Name   string
	Help   string
	Type   string
	Labels []Label
	Value  float64
}

type Label struct{ Name, Value string }

func (m Metric) write(sb *strings.Builder, namespace string) {
	name := namespace + "_" + m.Name
	sb.WriteString("# HELP " + name + " " + m.Help + "\n")
	sb.WriteString("# TYPE " + name + " " + m.Type + "\n")
	sb.WriteString(name)
	if len(m.Labels) > 0 {
		parts := make([]string, 0, len(m.Labels))
		for _, l := range m.Labels {
			parts = append(parts, l.Name+`="`+escapeLabel(l.Value)+`"`)
		}
		sb.WriteString("{" + strings.Join(parts, ",") + "}")
	}
	sb.WriteString(" " + formatFloat(m.Value) + "\n")
}

func labels(k key) string {
	return `{route="` + escapeLabel(k.route) +
		`",method="` + escapeLabel(k.method) +
		`",status="` + strconv.Itoa(k.status) + `"}`
}

// labelsWith adds one label to a series' set, for the histogram's le=.
func labelsWith(k key, name, value string) string {
	base := labels(k)
	return base[:len(base)-1] + `,` + name + `="` + escapeLabel(value) + `"}`
}

// escapeLabel escapes a label value per the exposition format: backslash,
// double quote and newline. Route templates and methods contain none of
// these, but build information comes from the build itself, and a value
// that breaks the format takes the whole scrape with it rather than one
// line.
func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// formatFloat writes a number without an exponent and without trailing
// zeroes. Prometheus accepts exponents; humans reading a scrape by hand
// find "1.5e-05" harder than "0.000015", and this endpoint is read both
// ways.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ContentType is what a scrape expects. The version parameter is part of
// it: a scraper reading a body without one falls back to guessing.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"
