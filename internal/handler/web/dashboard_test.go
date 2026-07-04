package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// dashboard_test.go はダッシュボード最小版 (GET / 、Plan
// dashboard-minimal.md) の web 層テスト。
//
// 4 ウィジェット:
//
//	(1) ライセンス保有・過不足 (v_license_usage、全ロール)
//	(2) 承認状況サマリ (default_approval_status 別製品数、全ロール)
//	(3) 満了間近ライセンス (90 日以内、general_user 以外)
//	(4) 退職者の未解除割当 (仕様 §5.14、license_manager 以上)
//
// 出し分けは §5.6 のウィジェット単位のみ (部署スコープは継続負債)。

// ウィジェット見出し。view 側の <h2> と一致させる。
const (
	headingUsage    = "ライセンス保有・過不足"
	headingApproval = "承認状況サマリ"
	headingExpiring = "満了間近ライセンス"
	headingLeaver   = "退職者の未解除割当"
)

// seedDashboardUserAssignment はライセンスへのアクティブなユーザ割当を
// 直接投入する (画面経由でない前提データ)。
func seedDashboardUserAssignment(t *testing.T, q *repository.Queries, licenseID, userID int64) repository.UserAssignment {
	t.Helper()
	ua, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: licenseID,
		UserID:    userID,
	})
	if err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}
	return ua
}

// --- 表示 (全ウィジェット) ------------------------------------------------

// system_admin は 4 ウィジェットすべての見出しが出る。ナビにも
// ダッシュボードリンク (先頭) が追加されている。
func TestDashboard_SystemAdmin_ShowsAllWidgets(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "DashVendor")
	seedApprovalProduct(t, q, v.ID, "DashApp", "globally_approved")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, headingUsage)
	handlertest.AssertContains(t, rec, headingApproval)
	handlertest.AssertContains(t, rec, headingExpiring)
	handlertest.AssertContains(t, rec, headingLeaver)
	// 承認状況サマリは default_approval_status の日本語ラベルと件数。
	handlertest.AssertContains(t, rec, "全社許可")
	// ナビの到達手段。
	handlertest.AssertContains(t, rec, `<a href="/">ダッシュボード</a>`)
}

// --- ロール別出し分け (§5.6) ----------------------------------------------

// viewer: 退職者ウィジェットは出ない (× 列)。満了間近は出る (〇 列)。
func TestDashboard_Viewer_HidesLeaverShowsExpiring(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, headingUsage)
	handlertest.AssertContains(t, rec, headingApproval)
	handlertest.AssertContains(t, rec, headingExpiring)
	if contains(rec.Body.String(), headingLeaver) {
		t.Errorf("viewer should not see %q widget:\n%s", headingLeaver, rec.Body.String())
	}
}

// general_user: 満了間近も退職者も出ない。(1)(2) は全社開示なので出る。
func TestDashboard_GeneralUser_HidesExpiringAndLeaver(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, headingUsage)
	handlertest.AssertContains(t, rec, headingApproval)
	if contains(rec.Body.String(), headingExpiring) {
		t.Errorf("general_user should not see %q widget:\n%s", headingExpiring, rec.Body.String())
	}
	if contains(rec.Body.String(), headingLeaver) {
		t.Errorf("general_user should not see %q widget:\n%s", headingLeaver, rec.Body.String())
	}
}

// license_manager: 退職者ウィジェットが出る (自部署列 = 表示対象ロール)。
func TestDashboard_LicenseManager_ShowsLeaver(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, headingLeaver)
	handlertest.AssertContains(t, rec, headingExpiring)
}

// 未ログインの GET / は既存 AuthMiddleware が /login へ 303 する。
func TestDashboard_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	r, _, _, _ := newWebRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("Location = %q, want prefix /login", loc)
	}
}

// --- 退職者の未解除割当 (§5.14) --------------------------------------------

// 退職済みユーザ (deactivated_at NOT NULL) にアクティブ割当が残っていれば
// 行として表示される。
func TestDashboard_LeaverAssignments_RowShown(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "LeaverVendor", "LeaverApp", "DEPT100", "総務部")
	lic := seedAssignLicense(t, q, s, "leaver", "退職者検出用契約", "user", nil)
	u := seedActiveUser(t, q, "E900", "退職者甲")
	seedDashboardUserAssignment(t, q, lic.ID, u.ID)
	if n, err := q.SoftDeleteUser(context.Background(), u.ID); err != nil || n != 1 {
		t.Fatalf("SoftDeleteUser: n=%d err=%v", n, err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "退職者甲")
	handlertest.AssertContains(t, rec, "LeaverApp")
}

// --- 満了間近ライセンス ------------------------------------------------------

// +30 日は出る。+200 日 (窓の外) と満了済みは出ない。
func TestDashboard_ExpiringLicenses_Window(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "ExpVendor", "ExpApp", "DEPT200", "経理部")
	// 実装の絞り込みは SQLite の date('now') (= UTC 日付) 基準。ローカル TZ の
	// time.Now() をそのまま使うと日付境界付近で 90 日窓の判定がズレて
	// フレークしうるため、UTC の日付 0 時を基準に組み立てる。
	nowUTC := time.Now().UTC()
	todayUTC := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	seedLicense(t, q, s, "soon", "満了30日前契約", timePtr(todayUTC.AddDate(0, 0, 30)), nil)
	seedLicense(t, q, s, "far", "満了200日前契約", timePtr(todayUTC.AddDate(0, 0, 200)), nil)
	seedLicense(t, q, s, "past", "満了済み契約", timePtr(todayUTC.AddDate(0, 0, -1)), nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "満了30日前契約")
	if body := rec.Body.String(); contains(body, "満了200日前契約") {
		t.Errorf("+200 days license should be outside the 90-day window:\n%s", body)
	}
	if body := rec.Body.String(); contains(body, "満了済み契約") {
		t.Errorf("expired license should not be listed:\n%s", body)
	}
}

// --- ライセンス保有・過不足 --------------------------------------------------

// 割当合計 > 保有 の製品には超過バッジが付く。
func TestDashboard_Usage_OverAllocatedBadge(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "OverVendor", "OverApp", "DEPT300", "開発部")
	lic := seedAssignLicense(t, q, s, "over", "超過検出用契約", "user", int64Ptr(1))
	u1 := seedActiveUser(t, q, "E301", "利用者甲")
	u2 := seedActiveUser(t, q, "E302", "利用者乙")
	seedDashboardUserAssignment(t, q, lic.ID, u1.ID)
	seedDashboardUserAssignment(t, q, lic.ID, u2.ID)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "OverApp")
	handlertest.AssertContains(t, rec, "超過")
}

// 無制限 (total_count NULL) のみ保有する製品は、v_license_usage 上
// total_owned=0 になるが超過扱いにしない (Plan 受け入れ基準)。
func TestDashboard_Usage_UnlimitedIsNotOverAllocated(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "UnlimVendor", "UnlimApp", "DEPT400", "営業部")
	lic := seedAssignLicense(t, q, s, "unlim", "無制限契約", "user", nil)
	u := seedActiveUser(t, q, "E401", "利用者丙")
	seedDashboardUserAssignment(t, q, lic.ID, u.ID)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "UnlimApp")
	handlertest.AssertContains(t, rec, "無制限")
	if body := rec.Body.String(); contains(body, "超過") {
		t.Errorf("unlimited-only product should not be flagged as over-allocated:\n%s", body)
	}
}

// 当日満了 (expires_at = 今日) のライセンスは「期限日当日は有効」の全体
// 方針どおり保有数に数えられ、偽の超過バッジが出ない。v_license_usage が
// > date('now') だと当日満了分が owned から消え、満了間近一覧 (>=) との
// 不整合で幻の超過が出る (PR #32 Copilot 指摘)。
func TestDashboard_Usage_CountsLicenseExpiringToday(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "TodayVendor", "TodayApp", "DEPT201", "総務部")
	nowUTC := time.Now().UTC()
	todayUTC := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	lic := seedAssignLicense(t, q, s, "today", "当日満了契約", "user", int64Ptr(5))
	// 日付のみの TEXT 値 (手動 SQL 編集や外部投入で入りうる形式) を使う。
	// Go ドライバ経由の時刻成分付き値は文字列比較の偶然で > date('now') を
	// 通過するが、日付のみだと除外され、偽の超過が出る (これが本バグ)。
	if _, err := db.Exec(`UPDATE licenses SET expires_at = ? WHERE id = ?`, todayUTC.Format("2006-01-02"), lic.ID); err != nil {
		t.Fatalf("set expires_at: %v", err)
	}
	u := seedActiveUser(t, q, "E951", "当日満了利用者")
	seedDashboardUserAssignment(t, q, lic.ID, u.ID)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, "超過") {
		t.Errorf("license expiring today (owned 5, assigned 1) must not show over-allocation:\n%s", body)
	}
}
