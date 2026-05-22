package handler_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler"
	"github.com/tagawa0525/app_man/internal/view/static"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	return handler.NewRouter(handler.Deps{
		Logger:   slog.New(slog.DiscardHandler),
		DB:       nil, // 本 PR では handler から DB を触らない
		StaticFS: static.FS(),
	})
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

	r := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/no-such-path", nil)
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
