package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

func okHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestCSRFMiddleware_GET_PassThrough(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(method, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (method %s)", rec.Code, http.StatusOK, method)
			}
			if !called {
				t.Fatalf("next handler should be called for %s", method)
			}
		})
	}
}

func TestCSRFMiddleware_POST_WithoutToken_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("next handler must not be called without CSRF token")
	}
}

func TestCSRFMiddleware_POST_WithHeaderToken_Passes(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-CSRF-Token", middleware.DummyCSRFToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("next handler should be called with valid header token")
	}
}

func TestCSRFMiddleware_POST_WithFormToken_Passes(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	form := url.Values{}
	form.Set("_csrf", middleware.DummyCSRFToken)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("next handler should be called with valid form token")
	}
}

func TestCSRFMiddleware_POST_InvalidToken_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-CSRF-Token", "wrong-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("next handler must not be called with invalid token")
	}
}
