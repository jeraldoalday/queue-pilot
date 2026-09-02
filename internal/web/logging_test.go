package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs returns a logger writing JSON into a buffer, plus a reader
// for the lines it produced.
func captureLogs() (*slog.Logger, func() []map[string]any) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return log, func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				continue
			}
			out = append(out, m)
		}
		return out
	}
}

func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app", nil))

	if seen == "" {
		t.Fatal("the handler saw no request ID in its context")
	}
	if got := rec.Header().Get(requestIDHeader); got != seen {
		t.Errorf("%s header = %q, want the context's ID %q", requestIDHeader, got, seen)
	}
}

// Two requests must not share an ID, or correlating log lines by it would
// group unrelated work together.
func TestRequestIDsAreDistinct(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app", nil))

		got := rec.Header().Get(requestIDHeader)
		if seen[got] {
			t.Fatalf("request ID %q was issued twice", got)
		}
		seen[got] = true
	}
}

// A proxy that already stamped an ID keeps it, so one identifier spans
// the hop rather than the app inventing a second one.
func TestRequestIDAcceptsAnUpstreamValue(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.Header.Set(requestIDHeader, "edge-7f3a91b2")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != "edge-7f3a91b2" {
		t.Errorf("%s = %q, want the upstream value preserved", requestIDHeader, got)
	}
}

// The value is echoed into a header and written into logs, so anything
// that could forge a log line or split a header is discarded in favour of
// a fresh ID.
func TestRequestIDRejectsHostileUpstreamValues(t *testing.T) {
	hostile := []struct {
		name  string
		value string
	}{
		{"a newline could forge a log line", "abc\ndef"},
		{"a carriage return could split a header", "abc\r\nX-Admin: true"},
		{"a space breaks header parsing", "abc def"},
		{"quotes and braces are not identifiers", `{"level":"error"}`},
		{"an overlong value is a log-flooding tool", strings.Repeat("a", maxRequestIDLen+1)},
	}

	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	for _, tt := range hostile {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/app", nil)
			// Set directly on the map: http.Header.Set would itself reject
			// some of these, and the point is what the middleware does.
			req.Header[requestIDHeader] = []string{tt.value}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(requestIDHeader)
			if got == tt.value {
				t.Errorf("%s = %q, want it replaced with a generated ID", requestIDHeader, got)
			}
			if got == "" {
				t.Error("no request ID was issued at all")
			}
		})
	}
}

// A value at the length limit is still usable — the bound rejects abuse,
// not ordinary IDs, and a UUID with a prefix is close to it.
func TestRequestIDAcceptsAValueAtTheLimit(t *testing.T) {
	value := strings.Repeat("a", maxRequestIDLen)

	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	req.Header.Set(requestIDHeader, value)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != value {
		t.Errorf("a %d-character ID was rejected", maxRequestIDLen)
	}
}

func TestAccessLogRecordsTheOutcome(t *testing.T) {
	log, lines := captureLogs()

	h := RequestID(AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("hello"))
	})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/app/queues", nil))

	got := lines()
	if len(got) != 1 {
		t.Fatalf("got %d log lines, want exactly one per request", len(got))
	}
	entry := got[0]

	if entry["method"] != "POST" || entry["path"] != "/app/queues" {
		t.Errorf("logged %v %v, want POST /app/queues", entry["method"], entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Errorf("logged status %v, want 201", entry["status"])
	}
	if entry["bytes"] != float64(5) {
		t.Errorf("logged bytes %v, want 5", entry["bytes"])
	}
	if rid, _ := entry["request_id"].(string); rid == "" {
		t.Error("the access log line carries no request ID")
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Error("the access log line carries no duration")
	}
}

// A handler that writes a body without calling WriteHeader has sent a
// 200, and the log must say so rather than reporting a zero status.
func TestAccessLogDefaultsToTwoHundred(t *testing.T) {
	log, lines := captureLogs()

	h := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := lines()[0]["status"]; got != float64(http.StatusOK) {
		t.Errorf("logged status %v, want 200", got)
	}
}

// The severity has to match the outcome, or a log filtered to errors
// either misses the failures or is nothing but polling.
func TestAccessLogLevelFollowsTheStatus(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status int
		want   string
	}{
		{"a served page", "/app/queues", http.StatusOK, "INFO"},
		{"a rejected form", "/app/queues", http.StatusUnprocessableEntity, "WARN"},
		{"a missing page", "/app/queues/x", http.StatusNotFound, "WARN"},
		{"a failure", "/app/queues", http.StatusInternalServerError, "ERROR"},
		{"a console poll", "/app/queues/abc/board", http.StatusOK, "DEBUG"},
		{"a public display poll", "/q/acme/walk-ins/display/board", http.StatusOK, "DEBUG"},
		{"a ticket poll", "/q/acme/walk-ins/t/abc/status", http.StatusOK, "DEBUG"},
		{"the line list's poll", "/q/acme/lines", http.StatusOK, "DEBUG"},
		{"a failing poll is still a failure", "/app/queues/abc/board", http.StatusInternalServerError, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, lines := captureLogs()

			h := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))

			if got := lines()[0]["level"]; got != tt.want {
				t.Errorf("%s logged at %v, want %s", tt.path, got, tt.want)
			}
		})
	}
}

// A poll is a GET made by a timer. The same suffix reached by a form post
// is somebody doing something, and must not be demoted.
func TestAccessLogOnlyDemotesReads(t *testing.T) {
	log, lines := captureLogs()

	h := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/app/tickets/abc/status", nil))

	if got := lines()[0]["level"]; got != "INFO" {
		t.Errorf("a POST to a poll-shaped path logged at %v, want INFO", got)
	}
}

// A panic must still produce an access log line saying 500, or the one
// request that most needs explaining is the one missing from the log.
func TestAccessLogRecordsAPanickingRequest(t *testing.T) {
	log, lines := captureLogs()

	h := RequestID(AccessLog(log)(RecoverPanic(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/app", nil))

	var access, recovered map[string]any
	for _, entry := range lines() {
		switch entry["msg"] {
		case "request":
			access = entry
		case "panic recovered":
			recovered = entry
		}
	}

	if access == nil {
		t.Fatal("a panicking request produced no access log line")
	}
	if access["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("the panicking request logged status %v, want 500", access["status"])
	}
	if recovered == nil {
		t.Fatal("the panic itself was not logged")
	}
	// Both lines must carry the same ID, or the stack and the request it
	// came from cannot be tied together in a log with anything else in it.
	if access["request_id"] != recovered["request_id"] {
		t.Errorf("access log ID %v and panic log ID %v differ", access["request_id"], recovered["request_id"])
	}
}
