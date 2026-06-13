// Package handlertest は handler 系テストで共通利用するヘルパを提供する。
//
// 主に提供する API:
//
//   - NewTestDB(t): in-memory sqlite に全マイグレーションを適用して *sql.DB を返す
//   - AuthenticatedAs(t, db, store, role): app_user + role + session を作って Cookie を返す
//   - AuthenticatedRequest(t, db, store, ...) / AuthenticatedPostForm(...):
//     上記 Cookie を付けた *http.Request を組み立てる
//   - AssertStatus / AssertContains / AssertRedirect: テストでよく使う assertion
package handlertest

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

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
