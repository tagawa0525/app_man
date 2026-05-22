package handler

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoverer_Panic_RendersServerErrorTemplate(t *testing.T) {
	t.Parallel()

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	h := recoverer(slog.New(slog.DiscardHandler))(panicking)

	req := httptest.NewRequest(http.MethodGet, "/explode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if ct := rec.Header().Get("Content-Type"); !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("500 Internal Server Error")) {
		t.Fatalf("body does not contain '500 Internal Server Error':\n%s", rec.Body.String())
	}
}

func TestRecoverer_NoPanic_PassesThrough(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})
	h := recoverer(slog.New(slog.DiscardHandler))(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("inner handler should be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Fatalf("body = %q, want %q", got, "hello")
	}
}

func TestRecoverer_ErrAbortHandler_RePanics(t *testing.T) {
	t.Parallel()

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	})
	h := recoverer(slog.New(slog.DiscardHandler))(panicking)

	defer func() {
		rvr := recover()
		if rvr == nil {
			t.Fatal("recoverer should re-panic on http.ErrAbortHandler")
		}
		err, ok := rvr.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("re-panic value = %v, want http.ErrAbortHandler", rvr)
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
}

func TestRecoverer_NilLogger_Tolerated(t *testing.T) {
	t.Parallel()

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	})
	h := recoverer(nil)(panicking) // logger が nil でも 500 を返せる

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
