package web

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// RecoverPanic turns a panic in any handler into a 500, logged with its
// stack, instead of letting it escape.
//
// net/http already recovers panics so one bad request can't take the
// process down, but its recovery closes the connection without a
// response: the client sees a reset rather than an error, htmx swaps
// nothing, and the page silently stops working. A panic is a bug either
// way — this makes it a visible, debuggable one.
//
// It also writes the stack through the app's structured logger rather
// than to stderr via net/http's default, so a panic in production lands
// in the same place as every other error.
func RecoverPanic(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The wrapper is created up front so the deferred recovery can
			// ask it whether the response has already started.
			rw := &statusRecorder{ResponseWriter: w}

			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				// A panic after the response has begun cannot be turned
				// into a 500 — the status line is already sent. Log it and
				// let the connection break, which is the honest outcome:
				// the client has a truncated response either way.
				if rw.wrote {
					log.Error("panic after response started",
						"error", rec, "method", r.Method, "path", r.URL.Path,
						"stack", string(debug.Stack()))
					panic(rec)
				}

				log.Error("panic recovered",
					"error", rec, "method", r.Method, "path", r.URL.Path,
					"stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

// statusRecorder notes whether anything has been written yet, which is
// what decides if a recovered panic can still be answered with a 500.
type statusRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer, so
// wrapping doesn't quietly disable flushing or deadline control for
// anything added later.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
