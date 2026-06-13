package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

func TestAllRoles_MatchesValidRoles(t *testing.T) {
	t.Parallel()

	all := middleware.AllRoles()
	if len(all) != 5 {
		t.Fatalf("AllRoles length: want 5, got %d", len(all))
	}
	for _, r := range all {
		if !middleware.IsValidRole(r) {
			t.Errorf("AllRoles contains role %q that IsValidRole rejects", r)
		}
	}
	// 重複なし
	seen := make(map[middleware.Role]bool, len(all))
	for _, r := range all {
		if seen[r] {
			t.Errorf("AllRoles contains duplicate: %q", r)
		}
		seen[r] = true
	}
}

func TestRoleFrom_EmptyContext_DefaultsToGeneralUser(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := middleware.RoleFrom(req.Context()); got != middleware.RoleGeneralUser {
		t.Fatalf("role = %q, want %q", got, middleware.RoleGeneralUser)
	}
}

// withRole は context に role を詰めたリクエストを返す (ヘルパ)。
// AuthMiddleware を経由しない RequireRole 単体テスト用。
func withRole(req *http.Request, role middleware.Role) *http.Request {
	return req.WithContext(middleware.WithRole(req.Context(), role))
}

func TestRequireRole_Allowed_Passes(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	gate := middleware.RequireRole(middleware.RoleSystemAdmin, middleware.RoleLicenseManager)
	h := gate(inner)

	req := withRole(httptest.NewRequest(http.MethodGet, "/", nil), middleware.RoleLicenseManager)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("inner handler should be called when role is allowed")
	}
}

func TestRequireRole_Forbidden_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	gate := middleware.RequireRole(middleware.RoleSystemAdmin)
	h := gate(inner)

	req := withRole(httptest.NewRequest(http.MethodGet, "/", nil), middleware.RoleViewer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("inner handler should not be called when role is not allowed")
	}
}
