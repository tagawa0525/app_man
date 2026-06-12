package session_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/session"
)

func newStore(t *testing.T) (*session.SQLiteStore, *sql.DB) {
	t.Helper()
	db := handlertest.NewTestDB(t)
	return session.NewSQLiteStore(db), db
}

// fixedClock は時刻誤差を排除するためのテスト用ヘルパ。
// CURRENT_TIMESTAMP に頼らず、すべてのカラムを呼び出し側が指定する。
func fixedClock() time.Time {
	return time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
}

func mustNewID(t *testing.T) string {
	t.Helper()
	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return id
}

func mustNewCSRF(t *testing.T) string {
	t.Helper()
	tok, err := session.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	return tok
}

func TestSQLiteStore_CreateAndGet_Anonymous(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	now := fixedClock()
	want := session.Session{
		ID:         mustNewID(t),
		AppUserID:  nil,
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(8 * time.Hour),
	}

	if err := store.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != want.ID || got.CSRFToken != want.CSRFToken {
		t.Fatalf("Get mismatch: got=%+v want=%+v", got, want)
	}
	if got.AppUserID != nil {
		t.Fatalf("AppUserID should be nil for anonymous, got=%v", *got.AppUserID)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("ExpiresAt: got=%v want=%v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestSQLiteStore_CreateAndGet_Authenticated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, db := newStore(t)

	// app_users に 1 行入れて FK を満たす
	res, err := db.ExecContext(ctx, `INSERT INTO app_users (username, auth_type) VALUES (?, 'local')`, "admin")
	if err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	appUserID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	now := fixedClock()
	want := session.Session{
		ID:         mustNewID(t),
		AppUserID:  &appUserID,
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(8 * time.Hour),
	}
	if err := store.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AppUserID == nil || *got.AppUserID != appUserID {
		t.Fatalf("AppUserID: got=%v want=%d", got.AppUserID, appUserID)
	}
}

func TestSQLiteStore_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	_, err := store.GetByID(ctx, "nonexistent-id")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestSQLiteStore_Touch_UpdatesLastSeen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	now := fixedClock()
	s := session.Session{
		ID:         mustNewID(t),
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
	if err := store.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	later := now.Add(10 * time.Minute)
	if err := store.Touch(ctx, s.ID, later); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	got, err := store.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got.LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt: got=%v want=%v", got.LastSeenAt, later)
	}
	// CreatedAt は変わらない
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt should not change: got=%v want=%v", got.CreatedAt, now)
	}
}

func TestSQLiteStore_Rotate_KeepsRowReplacesID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	now := fixedClock()
	oldID := mustNewID(t)
	s := session.Session{
		ID:         oldID,
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
	if err := store.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	newID := mustNewID(t)
	if err := store.Rotate(ctx, oldID, newID); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if _, err := store.GetByID(ctx, oldID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old ID should be gone, err=%v", err)
	}
	got, err := store.GetByID(ctx, newID)
	if err != nil {
		t.Fatalf("GetByID(newID): %v", err)
	}
	// CSRF token は同じ (Rotate は ID 差し替えのみ)
	if got.CSRFToken != s.CSRFToken {
		t.Fatalf("CSRF token should be preserved: got=%q want=%q", got.CSRFToken, s.CSRFToken)
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	now := fixedClock()
	s := session.Session{
		ID:         mustNewID(t),
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
	if err := store.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.GetByID(ctx, s.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after Delete, err=%v want sql.ErrNoRows", err)
	}
}

func TestSQLiteStore_DeleteExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newStore(t)

	now := fixedClock()
	expired := session.Session{
		ID:         mustNewID(t),
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now.Add(-2 * time.Hour),
		LastSeenAt: now.Add(-2 * time.Hour),
		ExpiresAt:  now.Add(-1 * time.Hour),
	}
	alive := session.Session{
		ID:         mustNewID(t),
		CSRFToken:  mustNewCSRF(t),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
	if err := store.Create(ctx, expired); err != nil {
		t.Fatalf("Create expired: %v", err)
	}
	if err := store.Create(ctx, alive); err != nil {
		t.Fatalf("Create alive: %v", err)
	}

	n, err := store.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted count: got=%d want=1", n)
	}

	if _, err := store.GetByID(ctx, expired.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired session should be gone, err=%v", err)
	}
	if _, err := store.GetByID(ctx, alive.ID); err != nil {
		t.Fatalf("alive session should remain, err=%v", err)
	}
}
