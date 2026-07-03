package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/repository"
)

// app_settings の保持期間キー (仕様書 §5.11)。値は日数の整数文字列。
const (
	keyRetentionAuditLogs         = "retention_days_audit_logs"
	keyRetentionRawInstallations  = "retention_days_raw_installations"
	keyRetentionImportLogs        = "retention_days_import_logs"
	keyRetentionNotificationsSent = "retention_days_notifications_sent"
)

// 保持期間の既定値 (日)。仕様書 §5.11 の表と一致させる。app_settings に
// キーが無い場合に使う (設定 UI は後続フェーズで、キー不在が通常状態)。
const (
	defaultRetentionAuditLogs         = 1825
	defaultRetentionRawInstallations  = 365
	defaultRetentionImportLogs        = 1095
	defaultRetentionNotificationsSent = 365
)

// runPrune は db.Open + pruneAll の薄い合成。now は cutoff 計算の基準時刻
// (main が time.Now() を渡す)。テストは in-memory DB を注入できる pruneAll を
// 直接呼ぶため、この関数はテスト対象にしない。
func runPrune(ctx context.Context, deps clirun.Deps, now time.Time) error {
	sqlDB, closeDB, err := db.Open(deps.Cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if cerr := closeDB(); cerr != nil {
			deps.Logger.Error("close db", slog.Any("error", cerr))
		}
	}()
	return pruneAll(ctx, sqlDB, deps.Logger, now, deps.DryRun)
}

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
func pruneAll(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, now time.Time, dryRun bool) error {
	q := repository.New(sqlDB)

	// 4 キーの解決を削除開始前にすべて済ませる。1 つでも不正値があれば
	// どのテーブルも削除せず全体を中断する (保持期間の解釈ミスによる
	// 大量削除事故の防止)。
	daysRaw, err := resolveRetentionDays(ctx, q, keyRetentionRawInstallations, defaultRetentionRawInstallations)
	if err != nil {
		return err
	}
	daysImport, err := resolveRetentionDays(ctx, q, keyRetentionImportLogs, defaultRetentionImportLogs)
	if err != nil {
		return err
	}
	daysAudit, err := resolveRetentionDays(ctx, q, keyRetentionAuditLogs, defaultRetentionAuditLogs)
	if err != nil {
		return err
	}
	daysNotif, err := resolveRetentionDays(ctx, q, keyRetentionNotificationsSent, defaultRetentionNotificationsSent)
	if err != nil {
		return err
	}

	cutoffRaw := now.UTC().AddDate(0, 0, -daysRaw)
	cutoffImport := now.UTC().AddDate(0, 0, -daysImport)
	cutoffAudit := now.UTC().AddDate(0, 0, -daysAudit)
	cutoffNotif := now.UTC().AddDate(0, 0, -daysNotif)

	if dryRun {
		return dryRunPrune(ctx, q, logger, cutoffAudit, cutoffRaw, cutoffImport, cutoffNotif)
	}

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

// dryRunPrune は DELETE と同一条件の COUNT でテーブルごとの対象件数のみ
// ログに出す (受け入れ基準 17「対象件数のみ確認できる」)。1 行も削除しない。
func dryRunPrune(ctx context.Context, q *repository.Queries, logger *slog.Logger,
	cutoffAudit, cutoffRaw, cutoffImport, cutoffNotif time.Time) error {
	wouldAudit, err := q.CountPrunableAuditLogs(ctx, cutoffAudit)
	if err != nil {
		return fmt.Errorf("count audit_logs: %w", err)
	}
	wouldRaw, err := q.CountPrunableRawInstallations(ctx, cutoffRaw)
	if err != nil {
		return fmt.Errorf("count raw_installations: %w", err)
	}
	wouldImport, err := q.CountPrunableImportLogs(ctx, cutoffImport)
	if err != nil {
		return fmt.Errorf("count import_logs: %w", err)
	}
	wouldNotif, err := q.CountPrunableSentNotifications(ctx, &cutoffNotif)
	if err != nil {
		return fmt.Errorf("count notifications: %w", err)
	}
	logger.Info("prune dry-run",
		slog.Int64("would_delete_audit_logs", wouldAudit),
		slog.Int64("would_delete_raw_installations", wouldRaw),
		slog.Int64("would_delete_import_logs", wouldImport),
		slog.Int64("would_delete_notifications", wouldNotif),
	)
	return nil
}

// resolveRetentionDays は app_settings から key の保持日数を取得する。
// 行が無ければ defaultDays を返す (seed が無いためキー不在が通常状態)。
// 値が NULL / 空 / 非整数 / 0 以下なら error — 保持期間の解釈ミスによる
// 大量削除事故を防ぐため、呼び出し側は削除を開始せず全体を中断する。
// 「0 日 = 全部消す」という解釈も仕様に無いので拒否する。
func resolveRetentionDays(ctx context.Context, q *repository.Queries, key string, defaultDays int) (int, error) {
	setting, err := q.GetAppSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultDays, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get app_setting %s: %w", key, err)
	}
	if setting.Value == nil || *setting.Value == "" {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got NULL or empty", key)
	}
	days, err := strconv.Atoi(*setting.Value)
	if err != nil {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got %q", key, *setting.Value)
	}
	if days <= 0 {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got %d", key, days)
	}
	return days, nil
}
