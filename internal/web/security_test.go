package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func headersFor(t *testing.T, path string, secure bool) http.Header {
	t.Helper()

	h := SecurityHeaders(secure)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Header()
}

func TestSecurityHeadersAreSet(t *testing.T) {
	got := headersFor(t, "/app", false)

	want := map[string]string{
		"Content-Security-Policy": contentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
	}
	for name, value := range want {
		if got.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, got.Get(name), value)
		}
	}
	if got.Get("Permissions-Policy") == "" {
		t.Error("Permissions-Policy is not set")
	}
}

// The policy has to permit what the pages actually do, or the app breaks
// in a way that only shows up in a browser console.
func TestContentSecurityPolicyMatchesWhatThePagesNeed(t *testing.T) {
	policy := contentSecurityPolicy

	// htmx is vendored under /static and loaded with a src attribute, so
	// same-origin scripts must be allowed.
	if !strings.Contains(policy, "script-src 'self'") {
		t.Error("the policy does not allow the vendored htmx to load")
	}
	// The report's histogram sizes each bar with a style attribute.
	if !strings.Contains(policy, "style-src 'self' 'unsafe-inline'") {
		t.Error("the policy would block the report histogram's bar widths")
	}
	// Nothing in the app needs either of these, and both turn an escaping
	// bug into script execution.
	for _, forbidden := range []string{"script-src 'self' 'unsafe-inline'", "'unsafe-eval'"} {
		if strings.Contains(policy, forbidden) {
			t.Errorf("the policy contains %q, which nothing in this app needs", forbidden)
		}
	}
	// An injected <base> would repoint every relative URL on the page,
	// including the form actions that move tickets.
	if !strings.Contains(policy, "base-uri 'self'") {
		t.Error("the policy does not pin base-uri")
	}
	if !strings.Contains(policy, "form-action 'self'") {
		t.Error("the policy does not pin form-action")
	}
}

// HSTS over plain HTTP would pin localhost to HTTPS in a developer's
// browser, which is tedious to undo.
func TestHSTSOnlyOverHTTPS(t *testing.T) {
	if got := headersFor(t, "/app", false).Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q on a plain-HTTP server, want it unset", got)
	}
	if got := headersFor(t, "/app", true).Get("Strict-Transport-Security"); got != hstsValue {
		t.Errorf("Strict-Transport-Security = %q, want %q", got, hstsValue)
	}
}

// The waiting-room display is meant to run on a screen in a room, and
// signage systems compose sources in frames. Every other page is a page
// with controls on it, and must not be embeddable.
func TestFrameOptionsAllowOnlyTheWaitingRoomDisplay(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/q/acme/walk-ins/display", "SAMEORIGIN"},
		{"/q/acme/walk-ins/display/board", "SAMEORIGIN"},
		{"/q/acme/walk-ins", "DENY"},
		{"/q/acme/walk-ins/board", "DENY"},
		{"/q/acme/walk-ins/t/abc", "DENY"},
		{"/app", "DENY"},
		{"/app/queues/abc/console", "DENY"},
		{"/login", "DENY"},
		{"/", "DENY"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := headersFor(t, tt.path, false).Get("X-Frame-Options"); got != tt.want {
				t.Errorf("X-Frame-Options on %s = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// An authenticated path that merely ends in "/display" must not be
// embeddable: the exception is for the public display page, and the
// prefix is what establishes that.
func TestFrameOptionsExceptionIsScopedToPublicPaths(t *testing.T) {
	for _, path := range []string{"/app/display", "/app/queues/abc/display", "/display"} {
		if got := headersFor(t, path, false).Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options on %s = %q, want DENY", path, got)
		}
	}
}

// The headers must reach the error responses too — an error page is a
// page, and a 500 rendered without a policy is the one most likely to be
// showing something unexpected.
func TestSecurityHeadersSurviveAPanic(t *testing.T) {
	log, _ := captureLogs()

	h := RecoverPanic(log)(SecurityHeaders(false)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("the 500 response carries no Content-Security-Policy")
	}
	if rec.Header().Get("X-Content-Type-Options") == "" {
		t.Error("the 500 response carries no nosniff header")
	}
}
