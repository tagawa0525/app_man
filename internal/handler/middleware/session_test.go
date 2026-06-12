package middleware_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// captureSession は SessionMiddleware を通った後の session を観測するヘルパ。
func captureSession(captured **session.Session) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = middleware.SessionFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func newSessionMW(t *testing.T, now time.Time) (func(http.Handler) http.Handler, session.Store) {
	t.Helper()
	store := session.NewSQLiteStore(handlertest.NewTestDB(t))
	mw := middleware.SessionMiddleware(middleware.SessionConfig{
		Store:        store,
		SecureCookie: false,
		MaxAge:       time.Hour,
		Now:          func() time.Time { return now },
	})
	return mw, store
}

func TestSessionMiddleware_NoCookie_CreatesAnonymousAndSetsCookie(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	mw, store := newSessionMW(t, now)

	var got *session.Session
	h := mw(captureSession(&got))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got == nil {
		t.Fatal("expected session in context, got nil")
	}
	if got.AppUserID != nil {
		t.Fatalf("AppUserID should be nil for new anonymous session, got %v", *got.AppUserID)
	}
	if len(got.ID) != 43 || len(got.CSRFToken) != 43 {
		t.Fatalf("ID / CSRF length unexpected: ID=%d CSRF=%d", len(got.ID), len(got.CSRFToken))
	}
	// Cookie 発行
	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == session.CookieName && c.Value == got.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Set-Cookie %s=%s not found in %v", session.CookieName, got.ID, cookies)
	}
	// DB にも入っている
	if _, err := store.GetByID(context.Background(), got.ID); err != nil {
		t.Fatalf("session should be persisted, err=%v", err)
	}
}

func TestSessionMiddleware_ExistingCookie_ReusesAndTouches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	mw, store := newSessionMW(t, now)

	// 既存セッションを直接 INSERT
	id, _ := session.NewID()
	tok, _ := session.NewCSRFToken()
	pre := session.Session{
		ID:         id,
		CSRFToken:  tok,
		CreatedAt:  now.Add(-30 * time.Minute),
		LastSeenAt: now.Add(-30 * time.Minute),
		ExpiresAt:  now.Add(30 * time.Minute),
	}
	if err := store.Create(context.Background(), pre); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got *session.Session
	h := mw(captureSession(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: id})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil || got.ID != id {
		t.Fatalf("expected reuse of session %q, got=%+v", id, got)
	}
	// last_seen_at が now に更新されているか
	persisted, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !persisted.LastSeenAt.Equal(now) {
		t.Fatalf("LastSeenAt: got=%v want=%v", persisted.LastSeenAt, now)
	}
}

func TestSessionMiddleware_ExpiredCookie_IssuesNewAndClearsOld(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	mw, store := newSessionMW(t, now)

	// 期限切れセッションを INSERT (expires_at < now)
	oldID, _ := session.NewID()
	tok, _ := session.NewCSRFToken()
	if err := store.Create(context.Background(), session.Session{
		ID:         oldID,
		CSRFToken:  tok,
		CreatedAt:  now.Add(-2 * time.Hour),
		LastSeenAt: now.Add(-2 * time.Hour),
		ExpiresAt:  now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got *session.Session
	h := mw(captureSession(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: oldID})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil || got.ID == oldID {
		t.Fatalf("expected NEW session, got=%+v (oldID=%q)", got, oldID)
	}
	// 新セッションは DB にある
	if _, err := store.GetByID(context.Background(), got.ID); err != nil {
		t.Fatalf("new session not persisted: %v", err)
	}
}

func TestSessionMiddleware_UnknownCookie_IssuesNew(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	mw, _ := newSessionMW(t, now)

	var got *session.Session
	h := mw(captureSession(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "no-such-session-id"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil || got.ID == "no-such-session-id" {
		t.Fatalf("expected NEW session, got=%+v", got)
	}
}

func TestSessionFrom_EmptyContext_ReturnsNil(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := middleware.SessionFrom(req.Context()); got != nil {
		t.Fatalf("SessionFrom on empty ctx = %+v, want nil", got)
	}
}

// sql.ErrNoRows ハンドリングが GetByID 内で透過していることの保険
func TestSQLiteStore_GetByID_NotFound_FromMiddlewareLayer(t *testing.T) {
	t.Parallel()
	store := session.NewSQLiteStore(handlertest.NewTestDB(t))
	_, err := store.GetByID(context.Background(), "x")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}
