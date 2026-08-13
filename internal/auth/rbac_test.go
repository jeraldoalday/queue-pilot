package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jeraldoalday/queuepilot/internal/tenant"
)

func TestRequireRoleAllowsMatchingRole(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/app/staff/new", nil)
	req = req.WithContext(tenant.WithIdentity(req.Context(), "org-1", "user-1", tenant.RoleAdmin))
	rec := httptest.NewRecorder()

	RequireRole(tenant.RoleAdmin)(next).ServeHTTP(rec, req)

	if !called {
		t.Error("admin was rejected from an admin-only route")
	}
}

func TestRequireRoleRejectsWrongRole(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/app/staff/new", nil)
	req = req.WithContext(tenant.WithIdentity(req.Context(), "org-1", "user-1", tenant.RoleStaff))
	rec := httptest.NewRecorder()

	RequireRole(tenant.RoleAdmin)(next).ServeHTTP(rec, req)

	if called {
		t.Error("staff user was allowed onto an admin-only route")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireRoleRejectsMissingIdentity(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/app/staff/new", nil)
	rec := httptest.NewRecorder()

	RequireRole(tenant.RoleAdmin)(next).ServeHTTP(rec, req)

	if called {
		t.Error("request with no identity in context reached the handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
