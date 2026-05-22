package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

// captureRoleHandler は DummyAuthMiddleware を通った後の role を
// テストで観測するためのアシスタント。
func captureRoleHandler(captured *middleware.Role) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = middleware.RoleFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestDummyAuthMiddleware_NoHeader_DefaultsToGeneralUser(t *testing.T) {
	t.Parallel()

	var got middleware.Role
	h := middleware.DummyAuthMiddleware(captureRoleHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got != middleware.RoleGeneralUser {
		t.Fatalf("role = %q, want %q", got, middleware.RoleGeneralUser)
	}
}

func TestDummyAuthMiddleware_KnownRole_Passes(t *testing.T) {
	t.Parallel()

	cases := []middleware.Role{
		middleware.RoleSystemAdmin,
		middleware.RoleDepartmentSecurityAdmin,
		middleware.RoleLicenseManager,
		middleware.RoleViewer,
		middleware.RoleGeneralUser,
	}

	for _, role := range cases {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			var got middleware.Role
			h := middleware.DummyAuthMiddleware(captureRoleHandler(&got))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-User-Role", string(role))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got != role {
				t.Fatalf("role = %q, want %q", got, role)
			}
		})
	}
}

func TestDummyAuthMiddleware_UnknownRole_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.DummyAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Role", "supreme_overlord")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("next handler should not be called for unknown role")
	}
}

func TestDummyAuthMiddleware_FallsBackToCookie(t *testing.T) {
	t.Parallel()

	var got middleware.Role
	h := middleware.DummyAuthMiddleware(captureRoleHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.RoleCookieName, Value: string(middleware.RoleLicenseManager)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got != middleware.RoleLicenseManager {
		t.Fatalf("role = %q, want %q (should be derived from Cookie)", got, middleware.RoleLicenseManager)
	}
}

func TestDummyAuthMiddleware_HeaderTakesPrecedenceOverCookie(t *testing.T) {
	t.Parallel()

	var got middleware.Role
	h := middleware.DummyAuthMiddleware(captureRoleHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Role", string(middleware.RoleViewer))
	req.AddCookie(&http.Cookie{Name: middleware.RoleCookieName, Value: string(middleware.RoleSystemAdmin)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got != middleware.RoleViewer {
		t.Fatalf("role = %q, want %q (header should win)", got, middleware.RoleViewer)
	}
}

func TestDummyAuthMiddleware_InvalidCookieClearsCookieAndFallsBackToGeneral(t *testing.T) {
	t.Parallel()

	var got middleware.Role
	h := middleware.DummyAuthMiddleware(captureRoleHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.RoleCookieName, Value: "supreme_overlord"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (invalid cookie should not 403, only header does)", rec.Code, http.StatusOK)
	}
	if got != middleware.RoleGeneralUser {
		t.Fatalf("role = %q, want %q", got, middleware.RoleGeneralUser)
	}
	// Cookie 削除指示が Set-Cookie で返ること
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.RoleCookieName && c.MaxAge < 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Set-Cookie with MaxAge<0 to clear invalid cookie, got cookies=%v", rec.Result().Cookies())
	}
}

func TestRoleFrom_EmptyContext_DefaultsToGeneralUser(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := middleware.RoleFrom(req.Context()); got != middleware.RoleGeneralUser {
		t.Fatalf("role = %q, want %q", got, middleware.RoleGeneralUser)
	}
}

func TestRequireRole_Allowed_Passes(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	gate := middleware.RequireRole(middleware.RoleSystemAdmin, middleware.RoleLicenseManager)
	h := middleware.DummyAuthMiddleware(gate(inner))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Role", string(middleware.RoleLicenseManager))
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
	h := middleware.DummyAuthMiddleware(gate(inner))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Role", string(middleware.RoleViewer))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("inner handler should not be called when role is not allowed")
	}
}
