package web_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// settings_test.go は /admin/settings (仕様 §5.11 / Plan admin-settings.md)
// の web 層テスト。
//
//   - GET  /admin/settings              既知 5 キーの一覧 (既定値フォールバック表示)
//   - POST /admin/settings/{key}        更新 (TrimSpace → 正整数検証 → UPSERT + audit)
//   - POST /admin/settings/{key}/reset  既定値へ戻す (行 DELETE + audit)
//
// 認可は system_admin のみ。app_settings は key が PK で数値 id を持たない
// ため、audit_logs の entity_id は NULL、対象キーは diff_json の key が持つ。

// seedAppSetting は app_settings に (key, value) を直接 UPSERT する。
func seedAppSetting(t *testing.T, q *repository.Queries, key, value string) {
	t.Helper()
	if _, err := q.UpsertAppSetting(context.Background(), repository.UpsertAppSettingParams{
		Key:   key,
		Value: &value,
	}); err != nil {
		t.Fatalf("seed UpsertAppSetting(%s): %v", key, err)
	}
}

// settingAuditDiffs は entity_type=app_setting の audit 行 (entity_id IS
// NULL) の diff_json を occurred_at, id 順にパースして返す。app_settings は
// 数値 id を持たないため countAuditLogs / auditDiff (entity_id 一致) は
// 使えない。
func settingAuditDiffs(t *testing.T, db *sql.DB, action string) []map[string]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT diff_json FROM audit_logs
		 WHERE action = ? AND entity_type = 'app_setting' AND entity_id IS NULL
		 ORDER BY id`, action)
	if err != nil {
		t.Fatalf("select audit_logs for %s: %v", action, err)
	}
	defer func() { _ = rows.Close() }()
	var diffs []map[string]any
	for rows.Next() {
		var diff sql.NullString
		if err := rows.Scan(&diff); err != nil {
			t.Fatalf("scan diff_json for %s: %v", action, err)
		}
		if !diff.Valid {
			t.Fatalf("diff_json for %s is NULL, want JSON with key field", action)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(diff.String), &m); err != nil {
			t.Fatalf("diff_json for %s is not valid JSON: %v (raw: %s)", action, err, diff.String)
		}
		diffs = append(diffs, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_logs for %s: %v", action, err)
	}
	return diffs
}

// assertNotContains は body に substr が含まれないことを確認する
// (handlertest には否定形が無いためローカル定義)。
func assertNotContains(t *testing.T, rec *httptest.ResponseRecorder, substr string) {
	t.Helper()
	if strings.Contains(rec.Body.String(), substr) {
		t.Fatalf("body contains %q, want absent:\n%s", substr, rec.Body.String())
	}
}

// --- 認可 -------------------------------------------------------------

// /admin/settings は system_admin のみ (仕様 §5.11 / §6.1)。
func TestAdminSettings_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	// dept_security_admin は /admin/* に入れない。
	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_audit_logs",
		middleware.RoleDepartmentSecurityAdmin, url.Values{"value": {"30"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_audit_logs/reset",
		middleware.RoleDepartmentSecurityAdmin, url.Values{})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- 一覧 -------------------------------------------------------------

// 行が無いキーは「既定値 N を使用中」(仕様 §5.11 の既定値) を表示する。
func TestAdminSettings_List_ShowsDefaultsWhenUnset(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	for _, want := range []string{
		"notification_max_retry",
		"retention_days_audit_logs",
		"retention_days_raw_installations",
		"retention_days_import_logs",
		"retention_days_notifications_sent",
		"既定値 5 を使用中",
		"既定値 1825 を使用中",
		"既定値 365 を使用中",
		"既定値 1095 を使用中",
	} {
		handlertest.AssertContains(t, rec, want)
	}
	// 未設定キーにはリセットボタンを出さない (戻す対象の行が無い)。
	assertNotContains(t, rec, "/admin/settings/retention_days_audit_logs/reset")
}

// 設定済みキーは DB の値を表示し、リセットボタンを出す。
func TestAdminSettings_List_ShowsStoredValue(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedAppSetting(t, q, "retention_days_import_logs", "500")

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "500")
	handlertest.AssertContains(t, rec, "/admin/settings/retention_days_import_logs/reset")
	// 設定済みの行に既定値フォールバック表示は出ない。
	assertNotContains(t, rec, "既定値 1095 を使用中")
}

// DB にある未知キー行は読み取り専用で表末尾に表示する (編集・リセット不可)。
func TestAdminSettings_List_UnknownKeyReadOnly(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedAppSetting(t, q, "mystery_key", "42")

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "mystery_key")
	handlertest.AssertContains(t, rec, "42")
	// 更新フォームもリセットフォームも生成しない。
	assertNotContains(t, rec, "/admin/settings/mystery_key")
}

// --- 更新 -------------------------------------------------------------

// 前後空白つき入力は TrimSpace してから正整数検証し、正規化した値で保存
// する (prune-logs の resolveRetentionDays は trim しない厳格検証のため、
// 入口で空白を除去して exit 1 経路を塞ぐ)。
func TestAdminSettings_Update_TrimsAndStores(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_audit_logs",
		middleware.RoleSystemAdmin, url.Values{"value": {" 30 "}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/admin/settings")

	row, err := q.GetAppSetting(context.Background(), "retention_days_audit_logs")
	if err != nil {
		t.Fatalf("GetAppSetting after update: %v", err)
	}
	if row.Value == nil || *row.Value != "30" {
		t.Errorf("value = %v, want 30 (trim 済み)", row.Value)
	}
	if row.UpdatedByAppUserID == nil {
		t.Error("updated_by_app_user_id is nil, want session AppUserID")
	}

	// audit app_setting.change: 未設定からの変更なので old は省略。
	diffs := settingAuditDiffs(t, db, "app_setting.change")
	if len(diffs) != 1 {
		t.Fatalf("audit app_setting.change count = %d, want 1", len(diffs))
	}
	if got := diffs[0]["key"]; got != "retention_days_audit_logs" {
		t.Errorf("change diff key = %v, want retention_days_audit_logs", got)
	}
	if got := diffs[0]["new"]; got != "30" {
		t.Errorf("change diff new = %v, want 30", got)
	}
	if got, ok := diffs[0]["old"]; ok {
		t.Errorf("change diff old = %v, want omitted (未設定からの変更)", got)
	}
}

// 設定済みキーの更新は diff_json に old / new の両方が入る。
func TestAdminSettings_Update_RecordsOldAndNew(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedAppSetting(t, q, "retention_days_raw_installations", "60")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_raw_installations",
		middleware.RoleSystemAdmin, url.Values{"value": {"45"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/admin/settings")

	row, err := q.GetAppSetting(context.Background(), "retention_days_raw_installations")
	if err != nil {
		t.Fatalf("GetAppSetting after update: %v", err)
	}
	if row.Value == nil || *row.Value != "45" {
		t.Errorf("value = %v, want 45", row.Value)
	}

	diffs := settingAuditDiffs(t, db, "app_setting.change")
	if len(diffs) != 1 {
		t.Fatalf("audit app_setting.change count = %d, want 1", len(diffs))
	}
	if got := diffs[0]["old"]; got != "60" {
		t.Errorf("change diff old = %v, want 60", got)
	}
	if got := diffs[0]["new"]; got != "45" {
		t.Errorf("change diff new = %v, want 45", got)
	}
}

// 非整数・0・負数・空は 400 で DB 不変 (prune-logs と同じ「正整数のみ」基準)。
func TestAdminSettings_Update_InvalidValues_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	for _, bad := range []string{"abc", "0", "-1", ""} {
		req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_audit_logs",
			middleware.RoleSystemAdmin, url.Values{"value": {bad}})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST value=%q: status = %d, want 400", bad, rec.Code)
		}
		if _, err := q.GetAppSetting(context.Background(), "retention_days_audit_logs"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetAppSetting after value=%q = %v, want sql.ErrNoRows (保存されない)", bad, err)
		}
	}
	if diffs := settingAuditDiffs(t, db, "app_setting.change"); len(diffs) != 0 {
		t.Errorf("audit app_setting.change count = %d, want 0 (不正値は記録しない)", len(diffs))
	}
}

// 未知キーへの POST は 404 (固定リスト外のキーを作らせない)。
func TestAdminSettings_Update_UnknownKey_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/mystery_key",
		middleware.RoleSystemAdmin, url.Values{"value": {"30"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
	if _, err := q.GetAppSetting(context.Background(), "mystery_key"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetAppSetting(mystery_key) = %v, want sql.ErrNoRows", err)
	}
}

// --- リセット -----------------------------------------------------------

// リセットは行 DELETE + audit app_setting.reset。一覧は既定値フォール
// バック表示に戻る。
func TestAdminSettings_Reset_DeletesRowAndRecordsAudit(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedAppSetting(t, q, "retention_days_notifications_sent", "300")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_notifications_sent/reset",
		middleware.RoleSystemAdmin, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/admin/settings")

	if _, err := q.GetAppSetting(context.Background(), "retention_days_notifications_sent"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetAppSetting after reset = %v, want sql.ErrNoRows (行 DELETE)", err)
	}

	diffs := settingAuditDiffs(t, db, "app_setting.reset")
	if len(diffs) != 1 {
		t.Fatalf("audit app_setting.reset count = %d, want 1", len(diffs))
	}
	if got := diffs[0]["key"]; got != "retention_days_notifications_sent" {
		t.Errorf("reset diff key = %v, want retention_days_notifications_sent", got)
	}
	if got := diffs[0]["old"]; got != "300" {
		t.Errorf("reset diff old = %v, want 300", got)
	}

	// 一覧は「既定値 365 を使用中」に戻る。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/settings", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "既定値 365 を使用中")
}

// 行が無いキーのリセットは 404 (戻す対象が無い)。
func TestAdminSettings_Reset_NoRow_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/retention_days_audit_logs/reset",
		middleware.RoleSystemAdmin, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// 未知キーは行が DB にあってもリセット不可 (読み取り専用)。
func TestAdminSettings_Reset_UnknownKey_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedAppSetting(t, q, "mystery_key", "42")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/settings/mystery_key/reset",
		middleware.RoleSystemAdmin, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
	row, err := q.GetAppSetting(context.Background(), "mystery_key")
	if err != nil {
		t.Fatalf("GetAppSetting(mystery_key) = %v, want row to survive", err)
	}
	if row.Value == nil || *row.Value != "42" {
		t.Errorf("mystery_key value = %v, want 42 (未変更)", row.Value)
	}
}
