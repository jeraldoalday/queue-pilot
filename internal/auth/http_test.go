package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/session"
)

// newTestHandlers builds Handlers with a nil DB pool: safe here because
// GetSignup/GetLogin only render templates and never touch the pool or
// session store — verifying those code paths doesn't require Postgres.
func newTestHandlers() *Handlers {
	sessions := session.NewStore(nil, clock.Real{})
	return NewHandlers(nil, sessions, false, slog.Default())
}

func TestGetSignupRendersForm(t *testing.T) {
	h := newTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/signup", nil)
	rec := httptest.NewRecorder()

	h.GetSignup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `action="/signup"`) {
		t.Error("signup page missing signup form")
	}
	if !strings.Contains(body, "org_name") {
		t.Error("signup page missing org_name field")
	}
}

func TestGetLoginRendersForm(t *testing.T) {
	h := newTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()

	h.GetLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `action="/login"`) {
		t.Error("login page missing login form")
	}
}

func TestUserFacingErrorHidesInternalDetails(t *testing.T) {
	h := newTestHandlers()

	got := h.userFacingError(ErrSlugTaken)
	if !strings.Contains(got, "already taken") {
		t.Errorf("ErrSlugTaken message = %q, want mention of 'already taken'", got)
	}

	got = h.userFacingError(ErrInvalidCredentials)
	if !strings.Contains(got, "Incorrect") {
		t.Errorf("ErrInvalidCredentials message = %q, want mention of 'Incorrect'", got)
	}

	// An arbitrary internal error must not leak its text to the user.
	internal := errUnexpected{"pq: connection refused on internal replica db-7"}
	got = h.userFacingError(internal)
	if strings.Contains(got, "db-7") {
		t.Errorf("userFacingError leaked internal detail: %q", got)
	}
}

type errUnexpected struct{ s string }

func (e errUnexpected) Error() string { return e.s }
