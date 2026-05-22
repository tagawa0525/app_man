package handlertest_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

func TestNewRequest_SetsRoleHeader(t *testing.T) {
	t.Parallel()

	req := handlertest.NewRequest(t, http.MethodGet, "/", middleware.RoleSystemAdmin, nil)
	if got := req.Header.Get("X-User-Role"); got != string(middleware.RoleSystemAdmin) {
		t.Fatalf("X-User-Role = %q, want %q", got, middleware.RoleSystemAdmin)
	}
}

func TestNewRequest_EmptyRole_NoHeader(t *testing.T) {
	t.Parallel()

	req := handlertest.NewRequest(t, http.MethodGet, "/", "", nil)
	if got := req.Header.Get("X-User-Role"); got != "" {
		t.Fatalf("X-User-Role should not be set when role is empty, got %q", got)
	}
}

func TestPostForm_AutoFillsCSRFToken(t *testing.T) {
	t.Parallel()

	values := url.Values{}
	values.Set("name", "alice")

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, values)

	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if got := req.PostFormValue("_csrf"); got != middleware.DummyCSRFToken {
		t.Fatalf("_csrf = %q, want %q", got, middleware.DummyCSRFToken)
	}
	if got := req.PostFormValue("name"); got != "alice" {
		t.Fatalf("name = %q, want %q", got, "alice")
	}
}

func TestPostForm_IntegratesWithCSRFMiddleware(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	values := url.Values{}
	values.Set("name", "bob")
	req := handlertest.PostForm(t, "/", middleware.RoleSystemAdmin, values)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if !called {
		t.Fatal("handlertest.PostForm should pass through CSRFMiddleware")
	}
}

func TestAssertContains(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	_, _ = rec.WriteString("hello world")
	handlertest.AssertContains(t, rec, "world")
}

func TestAssertRedirect(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	rec.Header().Set("Location", "/next")
	rec.WriteHeader(http.StatusSeeOther)
	handlertest.AssertRedirect(t, rec, "/next")
}
