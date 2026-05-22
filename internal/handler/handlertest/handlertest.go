// Package handlertest は handler 系テストで共通利用するヘルパを提供する。
//
// PR-A 時点で形を固定し、PR-B 以降のテーブル別 handler テストで再利用する。
// 「role 付きリクエストの組立」「CSRF トークン込みフォーム POST」
// 「よく使う assertion」の 3 つを集約する。
//
// CLAUDE.md「3 回重複してから抽象化」の例外として、test helper は
// 最初の利用時から形を固定する価値が大きい (テスト記述の一貫性確保)。
// 詳細は docs/plans/snazzy-percolating-micali.md 参照。
package handlertest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

// NewRequest は role 付きの *http.Request を組み立てる。
// role が空文字なら X-User-Role ヘッダを付与せず (DummyAuthMiddleware が
// general_user にフォールバックする経路の検証に使える)。
func NewRequest(t *testing.T, method, target string, role middleware.Role, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if role != "" {
		req.Header.Set("X-User-Role", string(role))
	}
	return req
}

// PostForm は CSRF トークンを自動付与した application/x-www-form-urlencoded
// な POST リクエストを組み立てる。values に _csrf が含まれていればそちらを
// 尊重し、含まれていなければ DummyCSRFToken を埋める。
func PostForm(t *testing.T, target string, role middleware.Role, values url.Values) *http.Request {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	if values.Get("_csrf") == "" {
		values.Set("_csrf", middleware.DummyCSRFToken)
	}
	req := NewRequest(t, http.MethodPost, target, role, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// AssertStatus は ResponseRecorder の status を確認する。
func AssertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
	}
}

// AssertContains は ResponseRecorder の body に substr が含まれることを確認する。
func AssertContains(t *testing.T, rec *httptest.ResponseRecorder, substr string) {
	t.Helper()
	if !bytes.Contains(rec.Body.Bytes(), []byte(substr)) {
		t.Fatalf("body does not contain %q:\n%s", substr, rec.Body.String())
	}
}

// AssertRedirect は ResponseRecorder の Location ヘッダと 3xx status を確認する。
func AssertRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantLocation string) {
	t.Helper()
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != wantLocation {
		t.Fatalf("Location = %q, want %q", got, wantLocation)
	}
}
