package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecoverPanicReturns500(t *testing.T) {
	h := RecoverPanic(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/app/services/notauuid", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	// The client must not be shown the panic value or a stack trace.
	if body := w.Body.String(); strings.Contains(body, "boom") {
		t.Fatalf("response body leaked the panic value: %q", body)
	}
}

func TestRecoverPanicLeavesNormalResponsesAlone(t *testing.T) {
	h := RecoverPanic(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "fine")
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 — the middleware altered a normal response", w.Code)
	}
	if got := w.Header().Get("X-Test"); got != "yes" {
		t.Fatalf("header lost through the middleware: %q", got)
	}
	if got := w.Body.String(); got != "fine" {
		t.Fatalf("body = %q, want %q", got, "fine")
	}
}

// A panic partway through a rendered page cannot become a 500 — the status
// line is already on the wire. It must not be swallowed into a
// half-written page that looks like a success.
func TestRecoverPanicRepanicsAfterResponseStarted(t *testing.T) {
	h := RecoverPanic(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>partial")
		panic("boom mid-render")
	}))

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("a panic after the response started was swallowed")
		}
	}()

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestStatusRecorderUnwraps(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner}
	if rec.Unwrap() != http.ResponseWriter(inner) {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}
}
