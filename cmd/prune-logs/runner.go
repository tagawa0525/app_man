package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/tagawa0525/app_man/internal/repository"
)

// 保持期間の既定値 (日)。仕様書 §5.11 の表と一致させる。app_settings に
// キーが無い場合に使う (設定 UI は後続フェーズで、キー不在が通常状態)。
const (
	defaultRetentionAuditLogs         = 1825
	defaultRetentionRawInstallations  = 365
	defaultRetentionImportLogs        = 1095
	defaultRetentionNotificationsSent = 365
)

// pruneAll は保持期間を超過したレコードを 4 テーブルから物理削除する本体。
// runPrune (db.Open との合成) から切り離してあるのは、テストが
// handlertest.NewTestDB の in-memory DB を注入できる継ぎ目を作るため。
//
// cutoff は now.UTC().AddDate(0, 0, -days) で計算する (§8.6: 保存は UTC)。
// sqlc クエリには time.Time を直接バインドする (Plan の事前検証パターン A。
// datetime(?) ラップは誤動作するため禁止)。
//
// トランザクションは使わない: 日次で再実行される冪等な処理で、途中失敗で
// 一部だけ消えても次回実行で収束する。巨大 tx の保持時間も避けられる。
//
// 第 5 引数 (dry-run) は後続コミットで実装する。
func pruneAll(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, now time.Time, _ bool) error {
	q := repository.New(sqlDB)

	cutoffRaw := now.UTC().AddDate(0, 0, -defaultRetentionRawInstallations)
	cutoffImport := now.UTC().AddDate(0, 0, -defaultRetentionImportLogs)
	cutoffAudit := now.UTC().AddDate(0, 0, -defaultRetentionAuditLogs)
	cutoffNotif := now.UTC().AddDate(0, 0, -defaultRetentionNotificationsSent)

	// FK の子から先に削除する: raw_installations → import_logs。
	// audit_logs / notifications は独立なので後段でよい。
	deletedRaw, err := q.PruneRawInstallations(ctx, cutoffRaw)
	if err != nil {
		return fmt.Errorf("prune raw_installations: %w", err)
	}
	deletedImport, err := q.PruneImportLogs(ctx, cutoffImport)
	if err != nil {
		return fmt.Errorf("prune import_logs: %w", err)
	}
	deletedAudit, err := q.PruneAuditLogs(ctx, cutoffAudit)
	if err != nil {
		return fmt.Errorf("prune audit_logs: %w", err)
	}
	// sent_at は nullable カラムのため sqlc が *time.Time を推論する。
	deletedNotif, err := q.PruneSentNotifications(ctx, &cutoffNotif)
	if err != nil {
		return fmt.Errorf("prune notifications: %w", err)
	}

	// レコードの中身 (diff_json / body 等) はログに出さない。件数のみ (§8.5)。
	logger.Info("prune completed",
		slog.Int64("deleted_audit_logs", deletedAudit),
		slog.Int64("deleted_raw_installations", deletedRaw),
		slog.Int64("deleted_import_logs", deletedImport),
		slog.Int64("deleted_notifications", deletedNotif),
		slog.Int64("total", deletedAudit+deletedRaw+deletedImport+deletedNotif),
	)
	return nil
}
