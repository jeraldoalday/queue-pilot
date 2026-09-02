package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/platform/metrics"
)

// The metrics endpoint, and the two decisions behind it.
//
// It is off unless METRICS_TOKEN is set. Request counts per route are a
// description of a business's day — how many people took a number, how
// many booked, when the rush was — and this is a multi-tenant app whose
// other public endpoints were each argued into existence on the grounds
// that they carry nothing a waiting room would not already see. A scrape
// carries rather more than that, so it is not served to whoever asks.
//
// And the route label is a template, never a path. A time series exists
// per distinct label combination, so recording "/q/acme-dental/walk-ins"
// would create one per queue per org — unbounded, driven by strangers,
// and growing until the process is restarted. routeLabel maps a path onto
// a fixed set of shapes and answers "other" for anything it does not
// recognise, which is the safe direction to be wrong in.

// namespace prefixes every metric name, which is the convention for
// telling one application's series from another's in a shared Prometheus.
const namespace = "queuepilot"

// Metrics records one observation per request.
//
// It sits inside the access log for the same reason the access log sits
// inside RequestID: both want the status the handler actually produced,
// and the innermost wrapper is the one that sees it.
func Metrics(reg *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if reg == nil {
			// Nothing to record into. Returning the handler unwrapped
			// keeps the disabled case free rather than paying for a
			// timer and a wrapper on every request to discard both.
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			reg.Observe(routeLabel(r.URL.Path), r.Method, rec.status, time.Since(start))
		})
	}
}

// handleMetrics serves the scrape.
//
// A disabled endpoint answers 404 rather than 401. Whether this
// deployment collects metrics is not information a stranger needs, and a
// 401 says "there is something here" to every scanner that asks.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MetricsToken == "" || !metricsAuthorized(r, s.cfg.MetricsToken) {
		http.NotFound(w, r)
		return
	}

	build := Build()
	var sb strings.Builder
	s.metrics.Write(&sb, namespace,
		// The build is on the same page as the traffic it explains, so
		// "the error rate rose at 14:05" and "we deployed at 14:04" are
		// one query rather than two systems to correlate by hand.
		metrics.Metric{
			Name: "build_info",
			Help: "The build this process is running. Always 1; read the labels.",
			Type: "gauge",
			Labels: []metrics.Label{
				{Name: "revision", Value: build.Revision},
				{Name: "go_version", Value: build.Go},
				{Name: "modified", Value: boolLabel(build.Modified)},
			},
			Value: 1,
		},
		metrics.Metric{
			Name:  "uptime_seconds",
			Help:  "Seconds since this process started serving.",
			Type:  "gauge",
			Value: time.Since(s.startedAt).Seconds(),
		},
	)

	w.Header().Set("Content-Type", metrics.ContentType)
	// A scrape is never worth caching: the point of it is the value now.
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte(sb.String())); err != nil {
		s.log.Warn("handleMetrics: write", "error", err)
	}
}

// metricsAuthorized checks the bearer token in constant time.
//
// Constant time because a token compared with == leaks its prefix to
// anyone willing to make a few million requests, and this endpoint is the
// one place in the app where a shared secret is checked at all — every
// other credential is a session token looked up in the database.
func metricsAuthorized(r *http.Request, token string) bool {
	header := r.Header.Get("Authorization")
	presented, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(token)) == 1
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// routeLabel maps a request path onto the route template it matched.
//
// It is a hand-written table rather than something read back from the
// mux: net/http records the matched pattern on the request the mux hands
// its handler, which is a different request value from the one this
// middleware holds, so there is nothing to read. The cost is that a new
// route is not counted by name until it is added here — which is why the
// unknown case is "other" rather than the raw path. A missing label is a
// gap in a dashboard; a raw path is an unbounded series count.
func routeLabel(path string) string {
	segments := splitPath(path)
	if len(segments) == 0 {
		return "/"
	}

	switch segments[0] {
	case "static":
		// Every asset is one series. Which file was fetched is a question
		// for the access log, not for a metric.
		return "/static"

	case "q":
		return publicQueueRoute(segments)

	case "book":
		return publicBookingRoute(segments)

	case "api":
		return apiRoute(segments)

	case "app":
		return appRoute(segments)

	case "healthz", "version", "robots.txt", "login", "logout", "signup", "metrics":
		if len(segments) == 1 {
			return "/" + segments[0]
		}
	}

	return "other"
}

// publicQueueRoute covers everything under /q/.
func publicQueueRoute(segments []string) string {
	switch {
	case len(segments) == 2:
		return "/q/:org"
	case len(segments) == 3 && segments[2] == "lines":
		return "/q/:org/lines"
	case len(segments) == 3:
		return "/q/:org/:queue"
	case len(segments) == 4:
		switch segments[3] {
		case "board", "display", "tickets":
			return "/q/:org/:queue/" + segments[3]
		}
	case len(segments) == 5:
		switch {
		case segments[3] == "display" && segments[4] == "board":
			return "/q/:org/:queue/display/board"
		case segments[3] == "t":
			return "/q/:org/:queue/t/:ticket"
		}
	case len(segments) == 6 && segments[3] == "t":
		switch segments[5] {
		case "status", "cancel":
			return "/q/:org/:queue/t/:ticket/" + segments[5]
		}
	}
	return "other"
}

// publicBookingRoute covers everything under /book/.
func publicBookingRoute(segments []string) string {
	switch {
	case len(segments) == 2:
		return "/book/:org"
	case len(segments) == 3 && segments[2] != "a":
		return "/book/:org/:service"
	case len(segments) == 4 && segments[2] == "a":
		return "/book/:org/a/:appointment"
	case len(segments) == 5 && segments[2] == "a":
		switch segments[4] {
		case "cancel", "calendar.ics":
			return "/book/:org/a/:appointment/" + segments[4]
		}
	}
	return "other"
}

// apiRoute covers /api/q/… (the original published board payload) and
// everything under /api/v1/.
func apiRoute(segments []string) string {
	if len(segments) == 4 && segments[1] == "q" {
		return "/api/q/:org/:queue"
	}
	if len(segments) < 3 || segments[1] != "v1" || segments[2] != "orgs" {
		return "other"
	}

	// segments is [api v1 orgs :org …]; rest is what follows the org.
	rest := segments[4:]
	base := "/api/v1/orgs/:org"
	switch {
	case len(rest) == 0:
		return base
	case rest[0] == "queues":
		switch len(rest) {
		case 2:
			return base + "/queues/:queue"
		case 3:
			return base + "/queues/:queue/tickets"
		case 4:
			return base + "/queues/:queue/tickets/:ticket"
		case 5:
			if rest[4] == "cancel" {
				return base + "/queues/:queue/tickets/:ticket/cancel"
			}
		}
	case rest[0] == "services":
		if len(rest) == 3 {
			switch rest[2] {
			case "slots", "bookings":
				return base + "/services/:service/" + rest[2]
			}
		}
	case rest[0] == "appointments":
		switch len(rest) {
		case 2:
			return base + "/appointments/:appointment"
		case 3:
			switch rest[2] {
			case "cancel", "calendar.ics":
				return base + "/appointments/:appointment/" + rest[2]
			}
		}
	}
	return "other"
}

// appRoute covers the authenticated application.
//
// The IDs here are UUIDs the app generated, so the templating is by
// position: a path under /app has at most one variable segment per noun,
// and the words around it are what identify the page.
func appRoute(segments []string) string {
	if len(segments) == 1 {
		return "/app"
	}

	out := make([]string, 0, len(segments))
	out = append(out, "app")
	for _, segment := range segments[1:] {
		if isVariableSegment(segment) {
			out = append(out, ":id")
			continue
		}
		out = append(out, segment)
	}
	return "/" + strings.Join(out, "/")
}

// isVariableSegment reports whether a path segment is an identifier rather
// than a word. Everything the app puts in an /app URL is either a fixed
// noun or a UUID, so "looks like a UUID" is the whole test — and anything
// unrecognised is treated as one, which keeps the label count bounded when
// a hand-edited URL arrives.
func isVariableSegment(segment string) bool {
	if len(segment) != 36 {
		return !isWord(segment)
	}
	return true
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
