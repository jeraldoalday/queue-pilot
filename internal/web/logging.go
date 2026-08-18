package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/platform/id"
)

// requestIDHeader is both read and written. Reading it lets a proxy or a
// load balancer that already stamped an ID keep one identifier across the
// hop; writing it means a user reporting "the page broke at 11:40" can
// quote a value that appears verbatim in the logs.
const requestIDHeader = "X-Request-Id"

// maxRequestIDLen bounds an inbound ID. The value ends up in every log
// line for the request and in a response header, so an unbounded one is a
// way to write megabytes into the log from outside.
const maxRequestIDLen = 64

type requestIDKey struct{}

// RequestID gives every request an identifier and puts it in the context,
// the response header, and — through WithRequestLogger — every log line
// the request produces.
//
// Without one, a 500 in the log is a line with a path on it, and the three
// other lines that request wrote are indistinguishable from the lines of
// every other request in flight at the same moment. On a queue console
// polling every five seconds across a dozen counters, that is most of the
// log.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if rid == "" {
			rid = id.New()
		}

		w.Header().Set(requestIDHeader, rid)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, rid)))
	})
}

// RequestIDFrom reads the request's identifier, returning "" outside a
// request that went through the RequestID middleware.
func RequestIDFrom(ctx context.Context) string {
	rid, _ := ctx.Value(requestIDKey{}).(string)
	return rid
}

// sanitizeRequestID decides whether an inbound header is usable as an
// identifier.
//
// The value is echoed back in a response header and written into logs, so
// it is accepted only as a short run of characters that cannot break
// either: no control characters (which could forge a log line or split a
// header), nothing exotic, nothing long. Anything else is discarded and a
// fresh ID generated, which is strictly better than propagating a value
// that would make the logs harder to read than no value at all.
func sanitizeRequestID(v string) string {
	if v == "" || len(v) > maxRequestIDLen {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return v
}

// AccessLog records one line per request: what was asked for, what came
// back, and how long it took.
//
// It logs after the handler rather than before, so the line carries the
// outcome. A pair of before/after lines would double the volume and still
// need correlating, which is what the request ID is for.
//
// Polling endpoints are logged at debug rather than info. The console
// board and the public display re-fetch themselves every few seconds by
// design, and at info they bury every line that says something happened —
// a log nobody can read is one nobody checks.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			case isPoll(r):
				level = slog.LevelDebug
			}

			log.Log(r.Context(), level, "request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// isPoll reports whether a path is one of the endpoints a page re-fetches
// on a timer. These are matched by suffix because each exists under
// several prefixes (a console board, a public board, a display, a ticket),
// and the property that matters is the same for all of them: the request
// was made by a timer, not by a person.
func isPoll(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	for _, suffix := range []string{"/board", "/display/board", "/status"} {
		if strings.HasSuffix(r.URL.Path, suffix) {
			return true
		}
	}
	return false
}

// statusCapture records the status and size of a response so the access
// log can report what was actually sent.
type statusCapture struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (s *statusCapture) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if !s.wrote {
		// A handler that writes without calling WriteHeader has sent a
		// 200, which is what the field was initialised to.
		s.wrote = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap keeps http.ResponseController working through the wrapper, so
// flushing and deadline control are not quietly disabled by logging.
func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }
