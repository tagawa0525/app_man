package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/notify"
	"github.com/tagawa0525/app_man/internal/repository"
)

// errSendFailed は fake チャネルの送信失敗をシミュレートする sentinel。
var errSendFailed = errors.New("simulated send failure")

// fakeNotifier はテスト用の Notifier。failWith が非 nil なら常に失敗し、
// 成功時は受け取った Notification を記録する。
type fakeNotifier struct {
	failWith error
	sent     []notify.Notification
}

func (f *fakeNotifier) Send(_ context.Context, msg notify.Notification) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.sent = append(f.sent, msg)
	return nil
}

func ptr(s string) *string { return &s }

// fileChannel は単一 file チャネルの列を作る (mode=file 相当)。
func fileChannel(n notify.Notifier) []notify.NamedNotifier {
	return []notify.NamedNotifier{{Name: "file", Notifier: n}}
}

// seedCatalog は vendor + product + 現役部署を 1 組投入する (licenses の FK 前提)。
func seedCatalog(t *testing.T, q *repository.Queries) (productID, deptID int64) {
	t.Helper()
	ctx := context.Background()
	v, err := q.CreateVendor(ctx, repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("CreateVendor: %v", err)
	}
	p, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Acrobat Pro",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	d, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "情報システム部",
	})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	return p.ID, d.ID
}

// seedExpiringLicense は now から days 日後の日付ちょうどに満了する
// ライセンスを 1 行投入する。expires_at は Web ハンドラ / bootstrap と同じく
// time.Parse("2006-01-02", ...) の値を bind する (実データと同じ保存形式)。
func seedExpiringLicense(t *testing.T, q *repository.Queries, productID, deptID int64, days int, now time.Time) repository.License {
	t.Helper()
	exp, err := time.Parse("2006-01-02", now.UTC().AddDate(0, 0, days).Format("2006-01-02"))
	if err != nil {
		t.Fatalf("parse expiry date: %v", err)
	}
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        "2026-jouki",
		DisplayName:        "Acrobat 年間契約",
		CountUnit:          "device",
		ContractType:       "subscription",
		ExpiresAt:          &exp,
		FsDirPath:          "licenses/adobe/acrobat-pro/2026-jouki",
	})
	if err != nil {
		t.Fatalf("CreateLicense: %v", err)
	}
	return lic
}

// seedAppUser は app_user を 1 行投入する。
func seedAppUser(t *testing.T, q *repository.Queries, username string, notifyEmail *string, linkedUserID *int64) repository.AppUser {
	t.Helper()
	au, err := q.CreateAppUser(context.Background(), repository.CreateAppUserParams{
		Username:     username,
		LinkedUserID: linkedUserID,
		NotifyEmail:  notifyEmail,
		AuthType:     "local",
	})
	if err != nil {
		t.Fatalf("CreateAppUser (%s): %v", username, err)
	}
	return au
}

// grantRole は app_user にロールを付与する。deptID nil は全社ロール (system_admin)。
func grantRole(t *testing.T, q *repository.Queries, appUserID int64, deptID *int64, role string) {
	t.Helper()
	if _, err := q.CreateUserDepartmentRole(context.Background(), repository.CreateUserDepartmentRoleParams{
		AppUserID:    appUserID,
		DepartmentID: deptID,
		Role:         role,
	}); err != nil {
		t.Fatalf("CreateUserDepartmentRole (%s): %v", role, err)
	}
}

// seedUser は users (人事マスタ) を 1 行投入する (linked_user の email 解決用)。
func seedUser(t *testing.T, q *repository.Queries, code string, email *string) repository.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: code,
		Name:         "田中太郎",
		Email:        email,
	})
	if err != nil {
		t.Fatalf("CreateUser (%s): %v", code, err)
	}
	return u
}

// setAppSetting は app_settings に key / value を 1 行 INSERT する。
func setAppSetting(t *testing.T, sqlDB *sql.DB, key, value string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO app_settings (key, value) VALUES (?, ?)`, key, value); err != nil {
		t.Fatalf("insert app_setting %s: %v", key, err)
	}
}

// notifRow は notifications の検証用スナップショット。
type notifRow struct {
	ID                int64
	Kind              string
	Channel           string
	Recipient         string
	Status            string
	RetryCount        int64
	LastError         *string
	SentAt            *string
	RelatedEntityType *string
	RelatedEntityID   *int64
}

// fetchNotifications は notifications 全行を id 順に読み出す。
func fetchNotifications(t *testing.T, sqlDB *sql.DB) []notifRow {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT id, kind, channel, recipient, status, retry_count,
		last_error, CAST(sent_at AS TEXT), related_entity_type, related_entity_id
		FROM notifications ORDER BY id`)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close() //nolint:errcheck // 読取専用カーソル。エラーは rows.Err() で拾う
	var out []notifRow
	for rows.Next() {
		var r notifRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Channel, &r.Recipient, &r.Status, &r.RetryCount,
			&r.LastError, &r.SentAt, &r.RelatedEntityType, &r.RelatedEntityID); err != nil {
			t.Fatalf("scan notification: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestNotifyAll_SendsAndSuppressesDuplicates は満了 30 日ちょうどのライセンスに
// 対して license_manager (notify_email あり) へ送信され notifications に sent が
// 記録されること、再実行で同一イベントが重複送信されないことを確認する。
func TestNotifyAll_SendsAndSuppressesDuplicates(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	lic := seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 1 {
		t.Fatalf("notifications: want 1 row, got %d (%+v)", len(rows), rows)
	}
	r := rows[0]
	if r.Kind != "license_expiry_30" {
		t.Errorf("kind = %q, want license_expiry_30", r.Kind)
	}
	if r.Channel != "file" {
		t.Errorf("channel = %q, want file", r.Channel)
	}
	if r.Recipient != "mgr@example.com" {
		t.Errorf("recipient = %q, want mgr@example.com", r.Recipient)
	}
	if r.Status != "sent" || r.SentAt == nil {
		t.Errorf("status = %q / sent_at = %v, want sent with sent_at", r.Status, r.SentAt)
	}
	if r.RelatedEntityType == nil || *r.RelatedEntityType != "license" ||
		r.RelatedEntityID == nil || *r.RelatedEntityID != lic.ID {
		t.Errorf("related = (%v, %v), want (license, %d)", r.RelatedEntityType, r.RelatedEntityID, lic.ID)
	}

	if len(fake.sent) != 1 {
		t.Fatalf("fake.sent: want 1 message, got %d", len(fake.sent))
	}
	msg := fake.sent[0]
	if msg.ID != r.ID {
		t.Errorf("msg.ID = %d, want notification id %d", msg.ID, r.ID)
	}
	if msg.Recipient != "mgr@example.com" {
		t.Errorf("msg.Recipient = %q", msg.Recipient)
	}
	if !strings.Contains(msg.Subject, "Acrobat 年間契約") || !strings.Contains(msg.Subject, "30") {
		t.Errorf("subject should contain license name and days, got %q", msg.Subject)
	}
	expDate := now.AddDate(0, 0, 30).Format("2006-01-02")
	for _, want := range []string{"Acrobat 年間契約", "Acrobat Pro", "情報システム部", expDate, "30"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body should contain %q, got:\n%s", want, msg.Body)
		}
	}

	// 再実行: sent レコードがあるため新規レコードも再送も発生しない。
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll (2nd): %v", err)
	}
	if rows := fetchNotifications(t, sqlDB); len(rows) != 1 {
		t.Errorf("re-run must not create rows: want 1, got %d", len(rows))
	}
	if len(fake.sent) != 1 {
		t.Errorf("re-run must not send: want 1 message, got %d", len(fake.sent))
	}
}

// TestNotifyAll_FallsBackToLinkedUserEmail は notify_email が空の license_manager
// に対して linked_user の email へ送信されることを確認する (仕様 §5.9 の優先順位)。
func TestNotifyAll_FallsBackToLinkedUserEmail(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	u := seedUser(t, q, "E0001", ptr("tanaka@example.com"))
	mgr := seedAppUser(t, q, "mgr", nil, &u.ID)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 1 {
		t.Fatalf("notifications: want 1 row, got %d", len(rows))
	}
	if rows[0].Recipient != "tanaka@example.com" {
		t.Errorf("recipient = %q, want linked user email tanaka@example.com", rows[0].Recipient)
	}
}

// TestNotifyAll_SkipsRecipientWithoutEmail は notify_email も linked_user email も
// 無い宛先が warn ログ付きでスキップされ、レコードが作られないことを確認する。
func TestNotifyAll_SkipsRecipientWithoutEmail(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr-noemail", nil, nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	var buf bytes.Buffer
	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	if rows := fetchNotifications(t, sqlDB); len(rows) != 0 {
		t.Errorf("no record must be created for empty recipient, got %d rows", len(rows))
	}
	if len(fake.sent) != 0 {
		t.Errorf("nothing must be sent, got %d", len(fake.sent))
	}
	if !strings.Contains(buf.String(), "mgr-noemail") || !strings.Contains(buf.String(), "WARN") {
		t.Errorf("warn log with username expected, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"skipped_no_recipient":1`) {
		t.Errorf("summary log should count skipped_no_recipient=1, got: %s", buf.String())
	}
}

// TestNotifyAll_FallsBackToSystemAdminsWhenNoManager は license_manager 不在の
// 部署で system_admin 全員にフォールバック送信されることを確認する (Plan の判断)。
func TestNotifyAll_FallsBackToSystemAdminsWhenNoManager(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	admin := seedAppUser(t, q, "admin", ptr("admin@example.com"), nil)
	grantRole(t, q, admin.ID, nil, "system_admin")

	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 1 {
		t.Fatalf("notifications: want 1 row (system_admin fallback), got %d", len(rows))
	}
	if rows[0].Recipient != "admin@example.com" {
		t.Errorf("recipient = %q, want admin@example.com", rows[0].Recipient)
	}
	if rows[0].Status != "sent" {
		t.Errorf("status = %q, want sent", rows[0].Status)
	}
}

// TestNotifyAll_SendFailureRecordsFailedAndRetrySucceeds は送信失敗が
// failed + last_error で記録され exit 1 相当の error になること、その後の
// --retry-failed (成功) で sent に遷移することを確認する。
func TestNotifyAll_SendFailureRecordsFailedAndRetrySucceeds(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	failing := &fakeNotifier{failWith: errSendFailed}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(failing), []int{30}, now, false); err == nil {
		t.Fatal("notifyAll with failing channel: want error (exit 1), got nil")
	}

	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 1 {
		t.Fatalf("notifications: want 1 row, got %d", len(rows))
	}
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
	if rows[0].LastError == nil || !strings.Contains(*rows[0].LastError, "simulated send failure") {
		t.Errorf("last_error = %v, want to contain send error detail", rows[0].LastError)
	}
	if rows[0].SentAt != nil {
		t.Errorf("sent_at must stay NULL on failure, got %v", *rows[0].SentAt)
	}

	// 再送 (成功): sent に遷移し、宛先・件名・本文は記録済みの内容で送られる。
	good := &fakeNotifier{}
	if err := retryAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), false); err != nil {
		t.Fatalf("retryAll: %v", err)
	}
	rows = fetchNotifications(t, sqlDB)
	if rows[0].Status != "sent" || rows[0].SentAt == nil {
		t.Errorf("after retry: status = %q / sent_at = %v, want sent", rows[0].Status, rows[0].SentAt)
	}
	if rows[0].LastError != nil {
		t.Errorf("after retry: last_error must be cleared, got %v", *rows[0].LastError)
	}
	if len(good.sent) != 1 || good.sent[0].Recipient != "mgr@example.com" {
		t.Errorf("retry must send to recorded recipient, got %+v", good.sent)
	}
}

// TestRetryAll_GaveUpAtMaxRetryAndDailySummary は再送失敗で retry_count が
// notification_max_retry に到達すると gave_up に遷移し、次の通常実行で
// system_admin に日次サマリが 1 通送られること、同日 2 回目の実行では
// サマリが再作成されないことを確認する。
func TestRetryAll_GaveUpAtMaxRetryAndDailySummary(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	setAppSetting(t, sqlDB, "notification_max_retry", "1")
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")
	admin := seedAppUser(t, q, "admin", ptr("admin@example.com"), nil)
	grantRole(t, q, admin.ID, nil, "system_admin")

	// 初回送信失敗 → failed。
	failing := &fakeNotifier{failWith: errSendFailed}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(failing), []int{30}, now, false); err == nil {
		t.Fatal("notifyAll with failing channel: want error, got nil")
	}

	// 再送も失敗 → retry_count 1 が上限 1 に到達し gave_up。
	if err := retryAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(failing), false); err == nil {
		t.Fatal("retryAll reaching max retry: want error, got nil")
	}
	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 1 {
		t.Fatalf("notifications: want 1 row, got %d", len(rows))
	}
	if rows[0].Status != "gave_up" || rows[0].RetryCount != 1 {
		t.Fatalf("status = %q / retry_count = %d, want gave_up / 1", rows[0].Status, rows[0].RetryCount)
	}

	// 上限到達後の再送対象は空 (retry_count < max を満たさない)。
	good := &fakeNotifier{}
	if err := retryAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), false); err != nil {
		t.Fatalf("retryAll (no targets): %v", err)
	}
	if len(good.sent) != 0 {
		t.Fatalf("gave_up rows must not be retried, got %d sends", len(good.sent))
	}

	// 通常実行: gave_up サマリが system_admin へ送られる (満了通知の再作成と併走)。
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll (summary run): %v", err)
	}
	var summaries []notifRow
	for _, r := range fetchNotifications(t, sqlDB) {
		if r.Kind == "gave_up_summary" {
			summaries = append(summaries, r)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("gave_up_summary: want 1 row, got %d", len(summaries))
	}
	s := summaries[0]
	if s.Recipient != "admin@example.com" || s.Status != "sent" {
		t.Errorf("summary recipient/status = %q/%q, want admin@example.com/sent", s.Recipient, s.Status)
	}
	if s.RelatedEntityType != nil || s.RelatedEntityID != nil {
		t.Errorf("summary related_* must be NULL, got (%v, %v)", s.RelatedEntityType, s.RelatedEntityID)
	}
	var summaryMsg *notify.Notification
	for i := range good.sent {
		if good.sent[i].ID == s.ID {
			summaryMsg = &good.sent[i]
		}
	}
	if summaryMsg == nil {
		t.Fatalf("summary message not sent via notifier, sent: %+v", good.sent)
	}
	for _, want := range []string{"license_expiry_30", "mgr@example.com"} {
		if !strings.Contains(summaryMsg.Body, want) {
			t.Errorf("summary body should contain %q, got:\n%s", want, summaryMsg.Body)
		}
	}

	// 同日 2 回目: 未サマリの gave_up が新たに発生しても、当日分のサマリが
	// 作成済みなら再作成しない (日次 1 通)。
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, status, retry_count, last_attempted_at)
		VALUES ('license_expiry_30', 'file', 'x@example.com', 'gave_up', 1, datetime('now', '+1 second'))`); err != nil {
		t.Fatalf("insert extra gave_up: %v", err)
	}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll (same-day 2nd): %v", err)
	}
	count := 0
	for _, r := range fetchNotifications(t, sqlDB) {
		if r.Kind == "gave_up_summary" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("same-day 2nd run must not create another summary: want 1, got %d", count)
	}
}

// TestNotifyAll_DryRunWritesNothing は dry-run が notifications へ一切書かず、
// detected / would_send をログに出すのみであることを確認する。
func TestNotifyAll_DryRunWritesNothing(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	var buf bytes.Buffer
	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(fake), []int{30}, now, true); err != nil {
		t.Fatalf("notifyAll dry-run: %v", err)
	}

	if rows := fetchNotifications(t, sqlDB); len(rows) != 0 {
		t.Errorf("dry-run must not write notifications, got %d rows", len(rows))
	}
	if len(fake.sent) != 0 {
		t.Errorf("dry-run must not send, got %d", len(fake.sent))
	}
	for _, want := range []string{`"detected":1`, `"would_send":1`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("dry-run log should contain %s, got: %s", want, buf.String())
		}
	}
}

// TestRetryAll_DryRunCountsOnly は --retry-failed の dry-run が対象件数のみ
// ログに出し、送信もレコード更新もしないことを確認する。
func TestRetryAll_DryRunCountsOnly(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	failing := &fakeNotifier{failWith: errSendFailed}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(failing), []int{30}, now, false); err == nil {
		t.Fatal("notifyAll with failing channel: want error, got nil")
	}

	var buf bytes.Buffer
	good := &fakeNotifier{}
	if err := retryAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(good), true); err != nil {
		t.Fatalf("retryAll dry-run: %v", err)
	}
	if len(good.sent) != 0 {
		t.Errorf("dry-run must not send, got %d", len(good.sent))
	}
	rows := fetchNotifications(t, sqlDB)
	if rows[0].Status != "failed" || rows[0].RetryCount != 0 {
		t.Errorf("dry-run must not update rows: status = %q / retry_count = %d", rows[0].Status, rows[0].RetryCount)
	}
	if !strings.Contains(buf.String(), `"would_retry":1`) {
		t.Errorf("dry-run log should contain would_retry=1, got: %s", buf.String())
	}
}

// TestRetryAll_InvalidMaxRetrySetting は notification_max_retry の不正値で
// 再送を開始せず error (exit 1 相当) になることを確認する (prune-logs の
// resolveRetentionDays と同基準)。
func TestRetryAll_InvalidMaxRetrySetting(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()
	setAppSetting(t, sqlDB, "notification_max_retry", "zero")

	good := &fakeNotifier{}
	err := retryAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), false)
	if err == nil {
		t.Fatal("retryAll with invalid notification_max_retry: want error, got nil")
	}
	if !strings.Contains(err.Error(), "notification_max_retry") {
		t.Errorf("error should mention the setting key, got: %v", err)
	}
}

// TestNotifyAll_MultiChannelPartialSuccess は multi 相当 (2 チャネル) で片方
// だけ失敗したとき、宛先 × チャネルごとのレコードで sent / failed が分離
// されること、再送は失敗チャネルのみ・そのチャネルの Notifier で行われる
// こと、再実行は成功済みチャネルを重複送信しないことを確認する
// (チャネル別レコード方式)。
func TestNotifyAll_MultiChannelPartialSuccess(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	goodFile := &fakeNotifier{}
	badSMTP := &fakeNotifier{failWith: errSendFailed}
	chans := []notify.NamedNotifier{
		{Name: "file", Notifier: goodFile},
		{Name: "smtp", Notifier: badSMTP},
	}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), chans, []int{30}, now, false); err == nil {
		t.Fatal("notifyAll with one failing channel: want error (exit 1), got nil")
	}

	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 2 {
		t.Fatalf("notifications: want 2 rows (one per channel), got %d (%+v)", len(rows), rows)
	}
	byChannel := map[string]notifRow{}
	for _, r := range rows {
		byChannel[r.Channel] = r
	}
	if r := byChannel["file"]; r.Status != "sent" {
		t.Errorf("file channel status = %q, want sent (partial success recorded per channel)", r.Status)
	}
	if r := byChannel["smtp"]; r.Status != "failed" || r.LastError == nil {
		t.Errorf("smtp channel status = %q / last_error = %v, want failed with last_error", r.Status, r.LastError)
	}
	if len(goodFile.sent) != 1 {
		t.Errorf("file channel sends = %d, want 1", len(goodFile.sent))
	}

	// 再送: 失敗した smtp チャネルのレコードだけが、smtp の Notifier で送られる。
	fileRetry := &fakeNotifier{}
	smtpRetry := &fakeNotifier{}
	retryChans := []notify.NamedNotifier{
		{Name: "file", Notifier: fileRetry},
		{Name: "smtp", Notifier: smtpRetry},
	}
	if err := retryAll(ctx, sqlDB, slog.New(slog.DiscardHandler), retryChans, false); err != nil {
		t.Fatalf("retryAll: %v", err)
	}
	if len(fileRetry.sent) != 0 {
		t.Errorf("file channel must not be re-sent (already sent), got %d", len(fileRetry.sent))
	}
	if len(smtpRetry.sent) != 1 || smtpRetry.sent[0].Recipient != "mgr@example.com" {
		t.Errorf("smtp retry sends = %+v, want 1 message to mgr@example.com", smtpRetry.sent)
	}
	for _, r := range fetchNotifications(t, sqlDB) {
		if r.Status != "sent" {
			t.Errorf("row %d (channel %s) status = %q, want sent after retry", r.ID, r.Channel, r.Status)
		}
	}

	// 再実行: 両チャネルとも sent 済み → 新規レコードなし。
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), chans, []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll (re-run): %v", err)
	}
	if rows := fetchNotifications(t, sqlDB); len(rows) != 2 {
		t.Errorf("re-run must not create rows: want 2, got %d", len(rows))
	}
}

// TestRetryAll_SkipsSupersededFailed は「failed が残ったまま通常実行が同一
// イベントを再作成して sent になった」場合に、--retry-failed が古い failed
// を再送しない (skipped_superseded) ことを確認する。仕様の重複抑止は sent
// のみを見るため通常実行は failed を再作成するが、その後の再送と合わさる
// と二重送信になる — 同一 (kind, channel, recipient, related) の sent が
// あれば再送対象から除外する。
func TestRetryAll_SkipsSupersededFailed(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	mgr := seedAppUser(t, q, "mgr", ptr("mgr@example.com"), nil)
	grantRole(t, q, mgr.ID, &deptID, "license_manager")

	// 初回失敗 → failed が残る。
	failing := &fakeNotifier{failWith: errSendFailed}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(failing), []int{30}, now, false); err == nil {
		t.Fatal("notifyAll with failing channel: want error, got nil")
	}

	// チャネル復旧後の通常実行: sent が無いため再作成して送信成功。
	good := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll (recovered): %v", err)
	}
	rows := fetchNotifications(t, sqlDB)
	if len(rows) != 2 || rows[0].Status != "failed" || rows[1].Status != "sent" {
		t.Fatalf("precondition: want [failed, sent], got %+v", rows)
	}

	// 再送: 旧 failed は同一イベントの sent 存在により除外される。
	var buf bytes.Buffer
	retryFake := &fakeNotifier{}
	if err := retryAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(retryFake), false); err != nil {
		t.Fatalf("retryAll: %v", err)
	}
	if len(retryFake.sent) != 0 {
		t.Errorf("superseded failed must not be re-sent, got %d sends", len(retryFake.sent))
	}
	rows = fetchNotifications(t, sqlDB)
	if rows[0].Status != "failed" || rows[0].RetryCount != 0 {
		t.Errorf("superseded row must stay untouched: status = %q / retry_count = %d", rows[0].Status, rows[0].RetryCount)
	}
	if !strings.Contains(buf.String(), `"skipped_superseded":1`) {
		t.Errorf("retry log should count skipped_superseded=1, got: %s", buf.String())
	}
}

// TestNotifyAll_WarnsWhenNoRecipientCandidates は license_manager 不在かつ
// system_admin も不在で宛先候補がゼロのとき、静かにスキップせず warn ログ +
// skipped_no_recipient に計上されることを確認する (PR #37 Copilot 指摘)。
func TestNotifyAll_WarnsWhenNoRecipientCandidates(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()
	productID, deptID := seedCatalog(t, q)
	seedExpiringLicense(t, q, productID, deptID, 30, now)
	// app_users を 1 人も作らない: license_manager もフォールバック先の
	// system_admin も不在。

	var buf bytes.Buffer
	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(fake), []int{30}, now, false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	if rows := fetchNotifications(t, sqlDB); len(rows) != 0 {
		t.Errorf("no record must be created without recipients, got %d rows", len(rows))
	}
	if len(fake.sent) != 0 {
		t.Errorf("nothing must be sent, got %d", len(fake.sent))
	}
	if !strings.Contains(buf.String(), "WARN") {
		t.Errorf("warn log expected for zero recipient candidates, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"skipped_no_recipient":1`) {
		t.Errorf("summary log should count skipped_no_recipient=1, got: %s", buf.String())
	}
}

// TestNotifyAll_ResummarizesAfterFailedSummary は「サマリ送信が失敗した」
// 場合に、その失敗サマリがチェックポイントを進めず、翌日相当の通常実行で
// 同じ gave_up 行が再サマリされることを確認する。チェックポイントが status
// を見ないと、届いていないサマリの存在だけで以前の gave_up が将来のサマリ
// から永久に漏れる (PR #37 Copilot 指摘)。
func TestNotifyAll_ResummarizesAfterFailedSummary(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	ctx := context.Background()
	admin := seedAppUser(t, q, "admin", ptr("admin@example.com"), nil)
	grantRole(t, q, admin.ID, nil, "system_admin")

	// gave_up 行と、その後に作成されたが送信に失敗したサマリを投入する。
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, status, retry_count, last_attempted_at)
		VALUES ('license_expiry_30', 'file', 'mgr@example.com', 'gave_up', 1, datetime('now'))`); err != nil {
		t.Fatalf("insert gave_up: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, subject, body, status, last_attempted_at, created_at)
		VALUES ('gave_up_summary', 'file', 'admin@example.com', 's', 'b', 'failed',
		        datetime('now', '+1 second'), datetime('now', '+1 second'))`); err != nil {
		t.Fatalf("insert failed summary: %v", err)
	}

	// 翌日相当の通常実行: 当日 dedup (作成基準) は前日の失敗サマリを数えず、
	// チェックポイントは sent のサマリのみ → 同じ gave_up 行が再サマリされる。
	good := &fakeNotifier{}
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	if err := notifyAll(ctx, sqlDB, slog.New(slog.DiscardHandler), fileChannel(good), []int{30}, tomorrow, false); err != nil {
		t.Fatalf("notifyAll (next day): %v", err)
	}

	var sentSummaries []notifRow
	for _, r := range fetchNotifications(t, sqlDB) {
		if r.Kind == "gave_up_summary" && r.Status == "sent" {
			sentSummaries = append(sentSummaries, r)
		}
	}
	if len(sentSummaries) != 1 {
		t.Fatalf("sent gave_up_summary: want 1 (re-summarized despite failed summary), got %d", len(sentSummaries))
	}
	if len(good.sent) != 1 || !strings.Contains(good.sent[0].Body, "mgr@example.com") {
		t.Errorf("re-summary should list the gave_up recipient, got %+v", good.sent)
	}
}

// TestRunNotify_ModeOffIsNoOp は mode=off (チャネル不在) で検出ごとスキップ
// され、info ログのみで正常終了することを確認する (DB にも触れない)。
func TestRunNotify_ModeOffIsNoOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	deps := clirun.Deps{
		// Database.Path を敢えて設定しない: off なら db.Open より手前で
		// return するため、DB 接続を試みた時点でテストが落ちる。
		Cfg:    &config.Config{Notifier: config.NotifierConfig{Mode: "off"}},
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	if err := runNotify(context.Background(), deps, false, time.Now()); err != nil {
		t.Fatalf("runNotify (mode=off): %v", err)
	}
	if !strings.Contains(buf.String(), "off") {
		t.Errorf("info log should mention mode off, got: %s", buf.String())
	}
}

// TestNotifyAll_GaveUpSummaryWarnsWhenNoAdmins は gave_up が存在するのに
// system_admin が不在 (または全員 email 空) のとき、サマリが静かに
// スキップされず warn + skipped_no_recipient に計上されることを確認する
// (PR #37 Copilot 指摘)。
func TestNotifyAll_GaveUpSummaryWarnsWhenNoAdmins(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()
	// gave_up 行のみ存在し、app_users は 1 人もいない。
	if _, err := sqlDB.Exec(`INSERT INTO notifications
		(kind, channel, recipient, status, retry_count, last_attempted_at)
		VALUES ('license_expiry_30', 'file', 'mgr@example.com', 'gave_up', 1, datetime('now'))`); err != nil {
		t.Fatalf("insert gave_up: %v", err)
	}

	var buf bytes.Buffer
	fake := &fakeNotifier{}
	if err := notifyAll(ctx, sqlDB, slog.New(slog.NewJSONHandler(&buf, nil)), fileChannel(fake), []int{30}, time.Now().UTC(), false); err != nil {
		t.Fatalf("notifyAll: %v", err)
	}

	for _, r := range fetchNotifications(t, sqlDB) {
		if r.Kind == "gave_up_summary" {
			t.Errorf("summary record must not be created without recipients: %+v", r)
		}
	}
	if len(fake.sent) != 0 {
		t.Errorf("nothing must be sent, got %d", len(fake.sent))
	}
	if !strings.Contains(buf.String(), "WARN") {
		t.Errorf("warn log expected for summary with zero admins, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"skipped_no_recipient":1`) {
		t.Errorf("summary log should count skipped_no_recipient=1, got: %s", buf.String())
	}
}
