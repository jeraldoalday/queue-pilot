package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/session"
)

func withTestIdentity(r *http.Request, csrfToken string) *http.Request {
	ident := session.Identity{
		UserID:    "user-1",
		OrgID:     "org-1",
		CSRFToken: csrfToken,
	}
	return r.WithContext(withIdentity(r.Context(), ident))
}

func TestRequireCSRFRejectsMissingToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodPost, "/app/staff", strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withTestIdentity(req, "correct-token")
	rec := httptest.NewRecorder()

	RequireCSRF(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Error("handler was called despite missing CSRF token")
	}
}

func TestRequireCSRFRejectsWrongToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	form := url.Values{"csrf_token": {"wrong-token"}}
	req := httptest.NewRequest(http.MethodPost, "/app/staff", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withTestIdentity(req, "correct-token")
	rec := httptest.NewRecorder()

	RequireCSRF(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if called {
		t.Error("handler was called despite wrong CSRF token")
	}
}

func TestRequireCSRFAcceptsMatchingToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	form := url.Values{"csrf_token": {"correct-token"}}
	req := httptest.NewRequest(http.MethodPost, "/app/staff", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withTestIdentity(req, "correct-token")
	rec := httptest.NewRecorder()

	RequireCSRF(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Error("handler was not called despite matching CSRF token")
	}
}

func TestRequireCSRFPassesThroughGET(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	// No identity in context at all — GET must still pass through, since
	// CSRF only applies to state-changing methods.
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()

	RequireCSRF(next).ServeHTTP(rec, req)

	if !called {
		t.Error("GET request was blocked by CSRF middleware")
	}
}
