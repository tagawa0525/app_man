package middleware_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// testMultipartLimit は multipart パース上限が主題でないテストで使う
// 十分大きな上限。
const testMultipartLimit = int64(1 << 20)

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
			h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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
	h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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
	h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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
	h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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
	h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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
	h := middleware.CSRFMiddleware(testMultipartLimit)(okHandler(&called))

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

// multipartCSRFRequest は hidden _csrf フィールドと filler バイトのファイル
// パートを持つ multipart POST を、session を context に埋めて組み立てる。
func multipartCSRFRequest(t *testing.T, formToken, sessionToken string, fillerSize int) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("_csrf", formToken); err != nil {
		t.Fatalf("write _csrf field: %v", err)
	}
	if fillerSize > 0 {
		fw, err := w.CreateFormFile("file", "filler.bin")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(bytes.Repeat([]byte("x"), fillerSize)); err != nil {
			t.Fatalf("write filler: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	sess := &session.Session{ID: "sess-id-1", CSRFToken: sessionToken}
	return req.WithContext(middleware.WithSessionForTest(req.Context(), sess))
}

// TestCSRFMiddleware_Multipart_WithFormToken_Passes は multipart form の
// hidden _csrf が上限内でパース・検証されることを確認する。
func TestCSRFMiddleware_Multipart_WithFormToken_Passes(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(1 << 20)(okHandler(&called))

	req := multipartCSRFRequest(t, "valid-csrf-token", "valid-csrf-token", 1024)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatal("next handler should be called with valid multipart form token")
	}
}

// TestCSRFMiddleware_Multipart_OverLimit_RejectedBeforeHandler は、上限を
// 超える multipart body が CSRF 検証段階で拒否され (400/413)、handler に
// 到達しないことを確認する。上限なしだと CSRF 不一致の巨大 multipart でも
// 一時ファイルへ書き出してしまう (DoS 入口) ため、パース前に
// MaxBytesReader を掛ける必要がある。
func TestCSRFMiddleware_Multipart_OverLimit_RejectedBeforeHandler(t *testing.T) {
	t.Parallel()

	called := false
	h := middleware.CSRFMiddleware(4096)(okHandler(&called))

	req := multipartCSRFRequest(t, "valid-csrf-token", "valid-csrf-token", 64<<10)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 400 or 413 (body: %s)", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("next handler must not be called when the multipart body exceeds the limit")
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
