package main

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
)

// seedAuditLog は occurred_at を now からの相対日時 (例 "-2000 days") で
// 指定して audit_logs に 1 行 INSERT する。
func seedAuditLog(t *testing.T, sqlDB *sql.DB, offset string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO audit_logs (action, entity_type, occurred_at)
		 VALUES ('update', 'license', datetime('now', ?))`, offset); err != nil {
		t.Fatalf("seed audit_log (%s): %v", offset, err)
	}
}

// seedImportLog は imported_at を相対日時で指定して import_logs に 1 行
// INSERT し、raw_installations の親にできるよう id を返す。
func seedImportLog(t *testing.T, sqlDB *sql.DB, offset string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(
		`INSERT INTO import_logs (source_type, source_file, imported_at, status)
		 VALUES ('skysea', 'export.csv', datetime('now', ?), 'success')`, offset)
	if err != nil {
		t.Fatalf("seed import_log (%s): %v", offset, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

// seedRawInstallation は created_at を相対日時で指定して raw_installations に
// 1 行 INSERT する。import_log_id は NOT NULL の FK なので親の id を要求する。
func seedRawInstallation(t *testing.T, sqlDB *sql.DB, importLogID int64, offset string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO raw_installations (import_log_id, device_asset_code, raw_product_name, created_at)
		 VALUES (?, 'PC-0001', 'SomeApp', datetime('now', ?))`, importLogID, offset); err != nil {
		t.Fatalf("seed raw_installation (%s): %v", offset, err)
	}
}

// seedSentNotification は sent_at を相対日時で指定して送信済み notification を
// 1 行 INSERT する。
func seedSentNotification(t *testing.T, sqlDB *sql.DB, offset string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO notifications (kind, channel, recipient, status, sent_at)
		 VALUES ('unapproved_software', 'email', 'admin@example.com', 'sent', datetime('now', ?))`, offset); err != nil {
		t.Fatalf("seed sent notification (%s): %v", offset, err)
	}
}

// seedUnsentNotification は sent_at IS NULL (pending / failed 相当) の
// notification を created_at を相対日時で指定して 1 行 INSERT する。
// 再送管理の対象なので prune が消してはいけない行。
func seedUnsentNotification(t *testing.T, sqlDB *sql.DB, createdOffset string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO notifications (kind, channel, recipient, status, created_at)
		 VALUES ('unapproved_software', 'email', 'admin@example.com', 'pending', datetime('now', ?))`, createdOffset); err != nil {
		t.Fatalf("seed unsent notification (%s): %v", createdOffset, err)
	}
}

// countWhere は table のうち where 条件に一致する行数を返す。全件は "1=1"。
// table / where はテスト内リテラルのみを渡す (ユーザ入力は連結しない)。
func countWhere(t *testing.T, sqlDB *sql.DB, table, where string) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + where).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPruneAll_DefaultRetention は app_settings にキーが無い (seed が無く
// 本番でも通常の) 状態で、仕様書 §5.11 の既定保持期間により各テーブルの
// 超過行だけが消え、期間内の行が残ることを確認する:
//   - audit_logs: 1825 日 (occurred_at)
//   - raw_installations: 365 日 (created_at)
//   - import_logs: 1095 日 (imported_at)
//   - notifications: 365 日 (sent_at、送信済みのみ)
func TestPruneAll_DefaultRetention(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	// audit_logs: -2000 日は既定 1825 日を超過 → 削除。-1000 / -10 日は残る。
	seedAuditLog(t, sqlDB, "-2000 days")
	seedAuditLog(t, sqlDB, "-1000 days")
	seedAuditLog(t, sqlDB, "-10 days")

	// raw_installations: 親 import_log は期間内 (-10 日) に置き、
	// -400 日の子だけが既定 365 日超過で削除される。
	parentID := seedImportLog(t, sqlDB, "-10 days")
	seedRawInstallation(t, sqlDB, parentID, "-400 days")
	seedRawInstallation(t, sqlDB, parentID, "-10 days")

	// import_logs: 子を持たない -2000 日の行は既定 1095 日超過 → 削除。
	// 上の親 (-10 日) は残る。
	seedImportLog(t, sqlDB, "-2000 days")

	// notifications: 送信済み -400 日は既定 365 日超過 → 削除。
	// 送信済み -10 日は残る。未送信 (sent_at IS NULL) は古くても残る。
	seedSentNotification(t, sqlDB, "-400 days")
	seedSentNotification(t, sqlDB, "-10 days")
	seedUnsentNotification(t, sqlDB, "-2000 days")

	if err := pruneAll(ctx, sqlDB, slog.New(slog.DiscardHandler), time.Now(), false); err != nil {
		t.Fatalf("pruneAll: %v", err)
	}

	if got := countWhere(t, sqlDB, "audit_logs", "1=1"); got != 2 {
		t.Errorf("audit_logs: want 2 rows kept, got %d", got)
	}
	if got := countWhere(t, sqlDB, "audit_logs", "occurred_at < datetime('now', '-1825 days')"); got != 0 {
		t.Errorf("audit_logs: retention-exceeded rows should be gone, got %d", got)
	}
	if got := countWhere(t, sqlDB, "raw_installations", "1=1"); got != 1 {
		t.Errorf("raw_installations: want 1 row kept, got %d", got)
	}
	if got := countWhere(t, sqlDB, "raw_installations", "created_at < datetime('now', '-365 days')"); got != 0 {
		t.Errorf("raw_installations: retention-exceeded rows should be gone, got %d", got)
	}
	if got := countWhere(t, sqlDB, "import_logs", "1=1"); got != 1 {
		t.Errorf("import_logs: want 1 row kept, got %d", got)
	}
	if got := countWhere(t, sqlDB, "notifications", "sent_at IS NOT NULL"); got != 1 {
		t.Errorf("sent notifications: want 1 row kept, got %d", got)
	}
	if got := countWhere(t, sqlDB, "notifications", "sent_at IS NULL"); got != 1 {
		t.Errorf("unsent notifications must not be pruned: want 1, got %d", got)
	}
}
