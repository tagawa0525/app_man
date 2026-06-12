package web

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
)

// TestRotateAndBind_SessionRowMissing_RollsBack は
// rotateAndBind の :execrows 検証経路をユニットテストで触る。
//
// 統合テスト (auth_test.go) からは SessionMiddleware が常に有効な session を
// context に詰めるため、oldSessionID が DB に無い経路を再現できない。
// 本テストは authHandlers を直接組み立てて
// 「存在しない oldSessionID で rotateAndBind を呼ぶ」をシミュレートし、
// errSessionRowMissing で失敗し last_login_at が更新されないことを確認する。
func TestRotateAndBind_SessionRowMissing_RollsBack(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	ctx := context.Background()

	res, err := db.ExecContext(ctx,
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES ('admin', 'irrelevant', 'local')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	appUserID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	h := &authHandlers{
		db:     db,
		logger: slog.New(slog.DiscardHandler),
	}
	_, err = h.rotateAndBind(ctx, "nonexistent-session-id", appUserID)
	if !errors.Is(err, errSessionRowMissing) {
		t.Fatalf("err = %v, want errSessionRowMissing", err)
	}

	var lastLogin sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT last_login_at FROM app_users WHERE id = ?`, appUserID).Scan(&lastLogin); err != nil {
		t.Fatalf("query: %v", err)
	}
	if lastLogin.Valid {
		t.Fatal("last_login_at should NOT be set when rotation failed (tx rolled back)")
	}
}

// TestRotateAndBind_Success は通常経路の sanity check。
func TestRotateAndBind_Success(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	ctx := context.Background()

	res, err := db.ExecContext(ctx,
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES ('admin', 'irrelevant', 'local')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	appUserID, _ := res.LastInsertId()

	now := time.Now()
	_, err = db.ExecContext(ctx, `INSERT INTO sessions (id, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		"old-session-id", "csrf-token", now, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := &authHandlers{
		db:     db,
		logger: slog.New(slog.DiscardHandler),
	}
	newID, err := h.rotateAndBind(ctx, "old-session-id", appUserID)
	if err != nil {
		t.Fatalf("rotateAndBind: %v", err)
	}
	if newID == "" || newID == "old-session-id" {
		t.Fatalf("newID = %q, want fresh random string", newID)
	}

	var boundAppUser sql.NullInt64
	if err := db.QueryRowContext(ctx,
		`SELECT app_user_id FROM sessions WHERE id = ?`, newID).Scan(&boundAppUser); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if !boundAppUser.Valid || boundAppUser.Int64 != appUserID {
		t.Fatalf("session.app_user_id = %+v, want %d", boundAppUser, appUserID)
	}

	var lastLogin sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT last_login_at FROM app_users WHERE id = ?`, appUserID).Scan(&lastLogin); err != nil {
		t.Fatalf("query last_login_at: %v", err)
	}
	if !lastLogin.Valid {
		t.Fatal("last_login_at should be set on successful rotation")
	}
}
