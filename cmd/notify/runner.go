package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/notify"
	"github.com/tagawa0525/app_man/internal/repository"
)

// app_settings の再送上限キー (仕様書 §5.11)。値は正整数の文字列。
const (
	keyMaxRetry     = "notification_max_retry"
	defaultMaxRetry = 5
)

// kindGaveUpSummary は gave_up 日次サマリの notifications.kind。
const kindGaveUpSummary = "gave_up_summary"

// runNotify は notify.FromConfig + db.Open と本体 (notifyAll / retryAll) の
// 薄い合成。now は満了検出の基準時刻 (main が time.Now() を渡す)。テストは
// in-memory DB と fake チャネル列を注入できる本体を直接呼ぶため、この関数は
// mode=off の no-op 以外テスト対象にしない。
func runNotify(ctx context.Context, deps clirun.Deps, retryFailed bool, now time.Time) error {
	channels, err := notify.FromConfig(deps.Cfg.Notifier)
	if err != nil {
		return fmt.Errorf("build notifier: %w", err)
	}
	if len(channels) == 0 {
		// mode=off はチャネル不在 (FromConfig が空を返す設計)。検出ごと
		// スキップし、DB にも触れず正常終了する — 通知未設定の環境で
		// スケジューラ登録だけ先行しても無害 (Plan の判断)。
		deps.Logger.Info("notifier mode is off; skipping notification run")
		return nil
	}

	sqlDB, closeDB, err := db.Open(deps.Cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if cerr := closeDB(); cerr != nil {
			deps.Logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	if retryFailed {
		return retryAll(ctx, sqlDB, deps.Logger, channels, deps.DryRun)
	}
	return notifyAll(ctx, sqlDB, deps.Logger, channels,
		deps.Cfg.Notifier.ExpiryDaysBefore, now, deps.DryRun)
}

// notifyCounters は通常実行のサマリログ用の集計。
type notifyCounters struct {
	detected           int // 満了 N 日ちょうどで検出したライセンス数
	wouldSend          int // dry-run で送信対象になった宛先 x チャネル数
	sent               int
	failed             int
	skippedDup         int // sent 済み (イベント x チャネル) の重複抑止
	skippedNoRecipient int // notify_email / linked_user email が両方空
}

// notifyAll は通常実行 (日次) の本体。各 N ∈ expiryDays について満了 N 日
// ちょうどのライセンスを検出し、所管部署の license_manager (不在なら
// system_admin) へ通知する。末尾で gave_up 日次サマリを送る。
//
// レコードは宛先 × チャネルごとに作成する (チャネル別レコード方式)。multi
// の部分成功時は成功チャネルが sent / 失敗チャネルだけが failed になり、
// --retry-failed は失敗チャネルのみ再送する。重複抑止もチャネル単位
// ((kind, channel, related)) で判定し、部分成功の再実行は未達チャネル
// だけを再作成する。
//
// runNotify から切り離してあるのは、テストが handlertest.NewTestDB の
// in-memory DB と fake チャネル列を注入できる継ぎ目を作るため
// (generate-meta の generateAll と同流儀)。
//
// 1 レコードの送信失敗で中断せず全件処理する。失敗は failed + last_error で
// 記録済みのため --retry-failed で再送でき、failed が 1 件以上なら error を
// 返して exit 1 でスケジューラ / 運用者に通知する。
func notifyAll(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, channels []notify.NamedNotifier,
	expiryDays []int, now time.Time, dryRun bool) error {
	q := repository.New(sqlDB)
	var c notifyCounters

	for _, days := range expiryDays {
		// 対象日付は Go 側で計算する (日付粒度・当日込みセマンティクス)。
		// SQL 側の date('now') に依存しないため now 注入で決定論的になる。
		target := now.UTC().AddDate(0, 0, days).Format("2006-01-02")
		rows, err := q.ListLicensesExpiringOn(ctx, target)
		if err != nil {
			return fmt.Errorf("list licenses expiring on %s: %w", target, err)
		}
		kind := fmt.Sprintf("license_expiry_%d", days)
		for _, row := range rows {
			c.detected++
			entityType := "license"
			entityID := row.ID

			// チャネル別に sent 済みか判定し、未達チャネルだけ残す。
			var pending []notify.NamedNotifier
			for _, ch := range channels {
				dup, err := q.CountSentNotificationForEvent(ctx, repository.CountSentNotificationForEventParams{
					Kind:              kind,
					Channel:           ch.Name,
					RelatedEntityType: &entityType,
					RelatedEntityID:   &entityID,
				})
				if err != nil {
					return fmt.Errorf("count sent notifications for %s license %d channel %s: %w", kind, row.ID, ch.Name, err)
				}
				if dup > 0 {
					c.skippedDup++
					continue
				}
				pending = append(pending, ch)
			}
			if len(pending) == 0 {
				continue
			}

			emails, skipped, err := resolveDeptRecipients(ctx, q, logger, row.OwningDepartmentID)
			if err != nil {
				return err
			}
			c.skippedNoRecipient += skipped

			subject, body := expiryMessage(row, days)
			for _, ch := range pending {
				for _, email := range emails {
					if dryRun {
						c.wouldSend++
						continue
					}
					sendErr, err := deliver(ctx, q, ch.Notifier, kind, ch.Name, email, subject, body, &entityType, &entityID)
					if err != nil {
						return err
					}
					if sendErr != nil {
						c.failed++
						// 本文 (ライセンス情報) はログに出さない (§8.5)。
						logger.Warn("send notification failed",
							slog.String("kind", kind),
							slog.Int64("license_id", row.ID),
							slog.String("channel", ch.Name),
							slog.String("recipient", email),
							slog.Any("error", sendErr))
						continue
					}
					c.sent++
				}
			}
		}
	}

	if err := sendGaveUpSummary(ctx, q, logger, channels, now, dryRun, &c); err != nil {
		return err
	}

	msg := "notify completed"
	if dryRun {
		msg = "notify dry-run"
	}
	logger.Info(msg,
		slog.Int("detected", c.detected),
		slog.Int("would_send", c.wouldSend),
		slog.Int("sent", c.sent),
		slog.Int("failed", c.failed),
		slog.Int("skipped_dup", c.skippedDup),
		slog.Int("skipped_no_recipient", c.skippedNoRecipient),
	)
	if c.failed > 0 {
		return fmt.Errorf("notify: %d of %d sends failed", c.failed, c.sent+c.failed)
	}
	return nil
}

// sendGaveUpSummary は未サマリの gave_up がある場合に、system_admin 宛の
// 日次サマリを送る (仕様 §5.9「system_admin 向けの日次サマリに集約」)。
// related_* は NULL のため重複抑止は「当日 (UTC) 分のサマリ作成済みか」で
// 判定する (チャネル問わず日次 1 回)。集計は呼び出し側の notifyCounters に
// 加算する。
func sendGaveUpSummary(ctx context.Context, q *repository.Queries, logger *slog.Logger,
	channels []notify.NamedNotifier, now time.Time, dryRun bool, c *notifyCounters) error {
	rows, err := q.ListUnsummarizedGaveUp(ctx)
	if err != nil {
		return fmt.Errorf("list unsummarized gave_up notifications: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	u := now.UTC()
	dayStart := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	count, err := q.CountNotificationsByKindSince(ctx, repository.CountNotificationsByKindSinceParams{
		Kind:      kindGaveUpSummary,
		CreatedAt: dayStart,
	})
	if err != nil {
		return fmt.Errorf("count today's gave_up summaries: %w", err)
	}
	if count > 0 {
		logger.Info("gave_up summary already created today; skipping",
			slog.Int("gave_up", len(rows)))
		return nil
	}

	admins, err := q.ListSystemAdminEmails(ctx)
	if err != nil {
		return fmt.Errorf("list system admins: %w", err)
	}
	var emails []string
	for _, a := range admins {
		email := pickEmail(a.NotifyEmail, a.LinkedUserEmail)
		if email == "" {
			logger.Warn("system admin has no email; skipping",
				slog.String("username", a.Username))
			c.skippedNoRecipient++
			continue
		}
		emails = append(emails, email)
	}

	subject, body := gaveUpSummaryMessage(rows)
	for _, ch := range channels {
		for _, email := range emails {
			if dryRun {
				c.wouldSend++
				continue
			}
			sendErr, err := deliver(ctx, q, ch.Notifier, kindGaveUpSummary, ch.Name, email, subject, body, nil, nil)
			if err != nil {
				return err
			}
			if sendErr != nil {
				c.failed++
				logger.Warn("send gave_up summary failed",
					slog.String("channel", ch.Name),
					slog.String("recipient", email),
					slog.Any("error", sendErr))
				continue
			}
			c.sent++
		}
	}
	return nil
}

// retryAll は --retry-failed の本体。status='failed' かつ retry_count <
// notification_max_retry の通知を、記録済みの宛先・件名・本文で、レコードの
// channel に対応する Notifier から再送する。成功: sent / 失敗:
// retry_count++ (上限到達で gave_up)。同一 (kind, channel, recipient,
// related) の sent が存在する failed は再送せず skipped_superseded として
// 数える (通常実行が再作成・送達済みのイベントを二重送信しない)。dry-run
// は対象件数のみログに出す。
func retryAll(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, channels []notify.NamedNotifier, dryRun bool) error {
	q := repository.New(sqlDB)
	maxRetry, err := resolveMaxRetry(ctx, q)
	if err != nil {
		return err
	}
	rows, err := q.ListFailedNotificationsForRetry(ctx, int64(maxRetry))
	if err != nil {
		return fmt.Errorf("list failed notifications: %w", err)
	}
	superseded, err := q.CountSupersededFailedNotifications(ctx, int64(maxRetry))
	if err != nil {
		return fmt.Errorf("count superseded notifications: %w", err)
	}
	if dryRun {
		logger.Info("notify retry dry-run",
			slog.Int("would_retry", len(rows)),
			slog.Int64("skipped_superseded", superseded))
		return nil
	}

	byName := make(map[string]notify.Notifier, len(channels))
	for _, ch := range channels {
		byName[ch.Name] = ch.Notifier
	}

	sent, failed, gaveUp, skippedChannel := 0, 0, 0, 0
	for _, row := range rows {
		notifier, ok := byName[row.Channel]
		if !ok {
			// 設定変更でチャネルが外れたレコード。送信試行ではないため
			// retry_count は増やさず、failed のまま残す。
			logger.Warn("channel not configured; skipping retry",
				slog.Int64("notification_id", row.ID),
				slog.String("channel", row.Channel))
			skippedChannel++
			continue
		}
		sendErr := notifier.Send(ctx, notify.Notification{
			ID:        row.ID,
			Recipient: row.Recipient,
			Subject:   strDeref(row.Subject),
			Body:      strDeref(row.Body),
		})
		if sendErr == nil {
			if _, err := q.MarkNotificationSent(ctx, row.ID); err != nil {
				return fmt.Errorf("mark notification %d sent: %w", row.ID, err)
			}
			sent++
			continue
		}
		status := "failed"
		if row.RetryCount+1 >= int64(maxRetry) {
			status = "gave_up"
		}
		errMsg := sendErr.Error()
		if _, err := q.IncrementNotificationRetry(ctx, repository.IncrementNotificationRetryParams{
			Status:    status,
			LastError: &errMsg,
			ID:        row.ID,
		}); err != nil {
			return fmt.Errorf("record retry failure for notification %d: %w", row.ID, err)
		}
		logger.Warn("retry send failed",
			slog.Int64("notification_id", row.ID),
			slog.String("channel", row.Channel),
			slog.Int64("retry_count", row.RetryCount+1),
			slog.String("status", status),
			slog.Any("error", sendErr))
		if status == "gave_up" {
			gaveUp++
		} else {
			failed++
		}
	}

	logger.Info("notify retry completed",
		slog.Int("targets", len(rows)),
		slog.Int("sent", sent),
		slog.Int("failed", failed),
		slog.Int("gave_up", gaveUp),
		slog.Int64("skipped_superseded", superseded),
		slog.Int("skipped_unknown_channel", skippedChannel),
	)
	if failed+gaveUp > 0 {
		return fmt.Errorf("notify retry: %d of %d retries failed", failed+gaveUp, len(rows))
	}
	return nil
}

// deliver は「送信前に必ずレコード作成」(仕様 §5.9) のフロー 1 回分:
// pending 作成 → Send → 成功: sent / 失敗: failed + last_error。送信失敗は
// sendErr として返し (呼び出し側が warn ログと集計に使う)、DB 失敗のみ err
// で全体を中断させる。
func deliver(ctx context.Context, q *repository.Queries, notifier notify.Notifier,
	kind, channel, recipient, subject, body string, relType *string, relID *int64) (sendErr, err error) {
	rec, err := q.CreateNotification(ctx, repository.CreateNotificationParams{
		Kind:              kind,
		Channel:           channel,
		Recipient:         recipient,
		Subject:           &subject,
		Body:              &body,
		RelatedEntityType: relType,
		RelatedEntityID:   relID,
	})
	if err != nil {
		return nil, fmt.Errorf("create notification (%s): %w", kind, err)
	}
	sendErr = notifier.Send(ctx, notify.Notification{
		ID:        rec.ID,
		Recipient: recipient,
		Subject:   subject,
		Body:      body,
	})
	if sendErr != nil {
		errMsg := sendErr.Error()
		if _, uerr := q.MarkNotificationFailed(ctx, repository.MarkNotificationFailedParams{
			LastError: &errMsg,
			ID:        rec.ID,
		}); uerr != nil {
			return sendErr, fmt.Errorf("mark notification %d failed: %w", rec.ID, uerr)
		}
		return sendErr, nil
	}
	if _, uerr := q.MarkNotificationSent(ctx, rec.ID); uerr != nil {
		return nil, fmt.Errorf("mark notification %d sent: %w", rec.ID, uerr)
	}
	return nil, nil
}

// resolveDeptRecipients は所管部署の license_manager の宛先を解決する。
// 部署に有効な license_manager が 1 人もいなければ system_admin 全員に
// フォールバックする (仕様の「設定された宛先」は未実装のため、握りつぶさない
// 最小策。Plan の判断)。両方の email が空の候補者は warn を出してスキップし、
// skipped に数える。
func resolveDeptRecipients(ctx context.Context, q *repository.Queries, logger *slog.Logger,
	deptID int64) (emails []string, skipped int, err error) {
	type candidate struct {
		username    string
		notifyEmail *string
		linkedEmail *string
	}
	var cands []candidate

	mgrs, err := q.ListLicenseManagerEmailsForDepartment(ctx, &deptID)
	if err != nil {
		return nil, 0, fmt.Errorf("list license managers for department %d: %w", deptID, err)
	}
	for _, m := range mgrs {
		cands = append(cands, candidate{m.Username, m.NotifyEmail, m.LinkedUserEmail})
	}
	if len(cands) == 0 {
		admins, err := q.ListSystemAdminEmails(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("list system admins: %w", err)
		}
		logger.Info("no license_manager for department; falling back to system admins",
			slog.Int64("department_id", deptID),
			slog.Int("system_admins", len(admins)))
		for _, a := range admins {
			cands = append(cands, candidate{a.Username, a.NotifyEmail, a.LinkedUserEmail})
		}
	}

	for _, cand := range cands {
		email := pickEmail(cand.notifyEmail, cand.linkedEmail)
		if email == "" {
			logger.Warn("recipient has no email; skipping",
				slog.String("username", cand.username),
				slog.Int64("department_id", deptID))
			skipped++
			continue
		}
		emails = append(emails, email)
	}
	return emails, skipped, nil
}

// pickEmail は宛先解決の優先順位 (仕様 §5.9): notify_email → linked_user の
// email。両方空なら "" を返す (呼び出し側が warn + skip)。
func pickEmail(notifyEmail, linkedEmail *string) string {
	if notifyEmail != nil && *notifyEmail != "" {
		return *notifyEmail
	}
	if linkedEmail != nil && *linkedEmail != "" {
		return *linkedEmail
	}
	return ""
}

// expiryMessage は満了通知の件名・本文 (日本語プレーンテキスト) を組み立てる。
func expiryMessage(row repository.ListLicensesExpiringOnRow, days int) (subject, body string) {
	subject = fmt.Sprintf("【ライセンス満了 %d 日前】%s", days, row.DisplayName)
	expires := ""
	if row.ExpiresAt != nil { // クエリ条件により常に非 NULL だが防御的に
		expires = row.ExpiresAt.UTC().Format("2006-01-02")
	}
	body = fmt.Sprintf(`以下のライセンスが満了まで残り %d 日です。更新または解約の手続きを確認してください。

ライセンス: %s
製品: %s %s
所管部署: %s
満了日: %s
残り日数: %d 日

本メールは app-manager (appmgr-notify) による自動送信です。
`, days, row.DisplayName, row.VendorName, row.ProductName, row.DepartmentName, expires, days)
	return subject, body
}

// gaveUpSummaryMessage は gave_up 日次サマリの件名・本文を組み立てる。
func gaveUpSummaryMessage(rows []repository.Notification) (subject, body string) {
	subject = fmt.Sprintf("【通知送信失敗サマリ】再送上限に達した通知 %d 件", len(rows))
	var b strings.Builder
	b.WriteString("再送上限に達し送信を断念した通知の一覧です。宛先とチャネルの設定を確認してください。\n\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "- id=%d kind=%s 宛先=%s", r.ID, r.Kind, r.Recipient)
		if r.LastError != nil && *r.LastError != "" {
			fmt.Fprintf(&b, " エラー: %s", *r.LastError)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n本メールは app-manager (appmgr-notify) による自動送信です。\n")
	return subject, b.String()
}

// resolveMaxRetry は app_settings から notification_max_retry を取得する。
// 行が無ければ既定値 5 (仕様 §5.11。seed が無くキー不在が通常状態)。値が
// NULL / 空 / 非整数 / 0 以下なら error — 上限の解釈ミスで再送が暴走したり
// 全件即 gave_up になる事故を防ぐため、再送を開始せず全体を中断する
// (prune-logs の resolveRetentionDays と同基準)。
func resolveMaxRetry(ctx context.Context, q *repository.Queries) (int, error) {
	setting, err := q.GetAppSetting(ctx, keyMaxRetry)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultMaxRetry, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get app_setting %s: %w", keyMaxRetry, err)
	}
	if setting.Value == nil || *setting.Value == "" {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got NULL or empty", keyMaxRetry)
	}
	n, err := strconv.Atoi(*setting.Value)
	if err != nil {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got %q", keyMaxRetry, *setting.Value)
	}
	if n <= 0 {
		return 0, fmt.Errorf("app_setting %s: value must be a positive integer, got %d", keyMaxRetry, n)
	}
	return n, nil
}

// strDeref は *string を空文字既定で外す。
func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
