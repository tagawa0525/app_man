package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

func okHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// requestWithSession は context に *session.Session を埋め込んだリクエストを返す。
// CSRFMiddleware が SessionMiddleware の後段で動く前提を再現する。
func requestWithSession(method, target, csrfToken string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	sess := &session.Session{ID: "sess-id-1", CSRFToken: csrfToken}
	return req.WithContext(middleware.WithSessionForTest(req.Context(), sess))
}

func TestCSRFMiddleware_GET_PassThrough(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			called := false
			h := middleware.CSRFMiddleware(okHandler(&called))

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

func TestCSRFMiddleware_POST_WithoutSession_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (no session in ctx)", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("next handler must not be called without session")
	}
}

func TestCSRFMiddleware_POST_WithHeaderToken_Passes(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := requestWithSession(http.MethodPost, "/", "valid-csrf-token", "")
	req.Header.Set("X-CSRF-Token", "valid-csrf-token")
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
	form.Set("_csrf", "valid-csrf-token")
	req := requestWithSession(http.MethodPost, "/", "valid-csrf-token", form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatal("next handler should be called with valid form token")
	}
}

func TestCSRFMiddleware_POST_InvalidToken_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	req := requestWithSession(http.MethodPost, "/", "valid-csrf-token", "")
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

func TestCSRFMiddleware_POST_EphemeralSession_Returns403(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(okHandler(&called))

	// session が存在するが CSRFToken が空 (ephemeral)
	// 空文字一致を偶発的に通さないためのガードを検証する
	req := requestWithSession(http.MethodPost, "/", "", "")
	req.Header.Set("X-CSRF-Token", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (ephemeral session must be rejected)", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("next handler must not be called for ephemeral session")
	}
}

func TestCSRFTokenFrom_NoSession_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	if got := middleware.CSRFTokenFrom(context.Background()); got != "" {
		t.Fatalf("CSRFTokenFrom() without session = %q, want empty", got)
	}
}

func TestCSRFTokenFrom_WithSession_ReturnsToken(t *testing.T) {
	t.Parallel()

	ctx := middleware.WithSessionForTest(context.Background(), &session.Session{CSRFToken: "abc-123"})
	if got := middleware.CSRFTokenFrom(ctx); got != "abc-123" {
		t.Fatalf("CSRFTokenFrom() = %q, want %q", got, "abc-123")
	}
}
