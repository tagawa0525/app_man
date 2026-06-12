package handler_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
	"github.com/tagawa0525/app_man/internal/view/static"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	r, _ := newAuthedTestRouter(t)
	return r
}

// newAuthedTestRouter は本番 router と同等の middleware チェーンを組み立て、
// system_admin として認証済みの session Cookie を併せて返す。
// /healthz / /static/* は公開パスなので Cookie 不要だが、業務ルートを
// 触るテスト (NotFound テンプレ等) では Cookie を AddCookie する。
func newAuthedTestRouter(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	db := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(db)
	r := handler.NewRouter(handler.Deps{
		Logger:        slog.New(slog.DiscardHandler),
		DB:            db,
		StaticFS:      static.FS(),
		SessionStore:  store,
		SessionMaxAge: time.Hour,
	})
	cookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)
	return r, cookie
}

func TestRouter_Healthz_Returns200(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}

func TestRouter_StaticHTMX_ByteEqual(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	got, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	staticFS := static.FS()
	f, err := staticFS.Open("htmx.min.js")
	if err != nil {
		t.Fatalf("open htmx.min.js from embed: %v", err)
	}
	defer func() { _ = f.Close() }()
	want, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read embed: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("served bytes differ from embed (got %d bytes, want %d bytes)", len(got), len(want))
	}
}

func TestRouter_StaticCSS_Served(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("css body is empty")
	}
}

func TestRouter_Unknown_Returns404_WithTemplate(t *testing.T) {
	t.Parallel()

	r, cookie := newAuthedTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/no-such-path", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("404 Not Found")) {
		t.Fatalf("body does not contain '404 Not Found':\n%s", body)
	}
}
