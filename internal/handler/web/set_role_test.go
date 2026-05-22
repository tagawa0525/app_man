package web_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/repository"
)

func TestSetRole_SetsCookie_RedirectsToReferer(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleLicenseManager)},
	})
	req.Header.Set("Referer", "http://example.test/vendors")
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/vendors" {
		t.Errorf("Location = %q, want /vendors", loc)
	}
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.RoleCookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatalf("expected Set-Cookie %q, got cookies=%v", middleware.RoleCookieName, rec.Result().Cookies())
	}
	if found.Value != string(middleware.RoleLicenseManager) {
		t.Errorf("Cookie value = %q, want %q", found.Value, middleware.RoleLicenseManager)
	}
	if !found.HttpOnly {
		t.Errorf("Cookie should be HttpOnly")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie SameSite = %v, want Lax", found.SameSite)
	}
}

func TestSetRole_RedirectsToRoot_WhenNoReferer(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestSetRole_RejectsCrossOriginReferer_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	req.Header.Set("Referer", "https://evil.example/steal")
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (cross-origin referer must be rejected)", rec.Code)
	}
}

func TestSetRole_RejectsCrossOriginOrigin_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (cross-origin Origin header must be rejected)", rec.Code)
	}
}

func TestSetRole_AcceptsSameOriginOrigin(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	req.Header.Set("Origin", "http://example.test")
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func TestSetRole_RedirectsToRoot_WhenRefererHasEmptyPath(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	req.Header.Set("Referer", "http://example.test")
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want / (empty path must fall back to /)", loc)
	}
}

func TestSetRole_RejectsInvalidRole_400(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {"supreme_overlord"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.RoleCookieName && c.MaxAge >= 0 {
			t.Errorf("invalid role should not set a positive-lifetime cookie, got %#v", c)
		}
	}
}

func TestSetRole_RejectsWithoutCSRF_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	body := url.Values{"role": {string(middleware.RoleViewer)}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/__set_role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (CSRF protection should kick in)", rec.Code)
	}
}

func TestSetRole_ProductionMode_404(t *testing.T) {
	t.Parallel()

	// 本番想定で DevMode=false のルータを直接組み立てる (newWebRouter は
	// DevMode=true なので使えない)。
	sqlDB := handlertest.NewTestDB(t)
	r := chi.NewRouter()
	r.Use(middleware.DummyAuthMiddleware)
	r.Use(middleware.CSRFMiddleware)
	web.RegisterRoutes(r, web.Deps{
		Logger:  slog.New(slog.DiscardHandler),
		DB:      sqlDB,
		DevMode: false,
	})
	_ = repository.New(sqlDB)

	req := handlertest.PostForm(t, "/__set_role", "", url.Values{
		"role": {string(middleware.RoleViewer)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405 (route should not exist in production mode)", rec.Code)
	}
}

func TestSetRole_AcceptedForAllRoles(t *testing.T) {
	t.Parallel()
	roles := []middleware.Role{
		middleware.RoleSystemAdmin,
		middleware.RoleDepartmentSecurityAdmin,
		middleware.RoleLicenseManager,
		middleware.RoleViewer,
		middleware.RoleGeneralUser,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			r, _ := newWebRouter(t)

			req := handlertest.PostForm(t, "/__set_role", "", url.Values{
				"role": {string(role)},
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303 for role %q (body: %s)", rec.Code, role, rec.Body.String())
			}
		})
	}
}
