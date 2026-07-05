package web_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/session"
)

// app_users_test.go は /admin/app-users (仕様 §6.1 / Plan
// admin-app-users-roles.md) の web 層テスト。
//
//   - GET  /admin/app-users                     一覧 (username / auth_type /
//     連携ユーザ / notify_email / 状態 / アクティブロール数)
//   - POST /admin/app-users/{id}/disable        無効化 + セッション失効 + audit
//   - POST /admin/app-users/{id}/enable         再有効化 + audit
//   - POST /admin/app-users/{id}/notify-email   通知先変更 + audit
//
// 認可は system_admin のみ。作成 UI は無い (CLI appmgr-create-app-user の
// 責務)。自分自身の無効化は 400 (全 system_admin ロックアウトの最短経路)。
//
// 無効化と既存セッション (Plan 想定リスク): AuthMiddleware は毎リクエスト
// user_department_roles しか引かず app_users.disabled_at を見ないため、
// 無効化処理がそのユーザのセッションを DB から削除しないと操作を継続
// できてしまう。ここで「無効化 → 既存 Cookie でのアクセスが /login に
// 戻される」ことを契約として固定する。

// seedAppUserRow はローカル認証の app_users 行を 1 つ作る。
func seedAppUserRow(t *testing.T, q *repository.Queries, username string, notifyEmail *string, linkedUserID *int64) repository.AppUser {
	t.Helper()
	hash := ""
	u, err := q.CreateAppUser(context.Background(), repository.CreateAppUserParams{
		Username:     username,
		PasswordHash: &hash,
		LinkedUserID: linkedUserID,
		NotifyEmail:  notifyEmail,
		AuthType:     "local",
	})
	if err != nil {
		t.Fatalf("seed CreateAppUser(%s): %v", username, err)
	}
	return u
}

// sessionForCookie は Cookie に対応する session を返す (actor の
// app_user_id / CSRF token をテストから知るため)。
func sessionForCookie(t *testing.T, store session.Store, cookie *http.Cookie) *session.Session {
	t.Helper()
	sess, err := store.GetByID(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("GetByID(%q): %v", cookie.Value, err)
	}
	return sess
}

// postFormAs は既存の session Cookie を使い、その CSRF token を埋めた
// POST リクエストを組み立てる。handlertest.AuthenticatedPostForm は毎回
// 新しいユーザを作るため、「自分自身への操作」や「同一 actor での連続
// 操作」にはこちらを使う。
func postFormAs(t *testing.T, store session.Store, cookie *http.Cookie, target string, values url.Values) *http.Request {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	if values.Get("_csrf") == "" {
		values.Set("_csrf", sessionForCookie(t, store, cookie).CSRFToken)
	}
	encoded := values.Encode()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(encoded))
	req.ContentLength = int64(len(encoded))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	return req
}

// tableRowContaining は body から marker を含む <tr>...</tr> ブロックを
// 返す。行単位の表示検証 (「-」等の頻出文字列をセル単位で見る) に使う。
func tableRowContaining(t *testing.T, rec *httptest.ResponseRecorder, marker string) string {
	t.Helper()
	body := rec.Body.String()
	for _, seg := range strings.Split(body, "<tr>") {
		row, _, _ := strings.Cut(seg, "</tr>")
		if strings.Contains(row, marker) {
			return row
		}
	}
	t.Fatalf("no <tr> containing %q in body:\n%s", marker, body)
	return ""
}

// --- 認可 -------------------------------------------------------------

// /admin/app-users は system_admin のみ (仕様 §6.1)。
func TestAdminAppUsers_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "authz_target", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/app-users", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/disable",
		middleware.RoleDepartmentSecurityAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/app-users", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- 一覧 -------------------------------------------------------------

// 一覧は username / auth_type / 連携ユーザ名 / notify_email / 状態 /
// アクティブロール数を表示する。
func TestAdminAppUsers_List(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// 連携ユーザ (users) を持つ app_user。
	person, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E0001",
		Name:         "山田 太郎",
	})
	if err != nil {
		t.Fatalf("seed CreateUser: %v", err)
	}
	email := "alice@example.com"
	alice := seedAppUserRow(t, q, "alice_linked", &email, &person.ID)
	dept := seedApprovalDept(t, q)
	for _, role := range []string{"viewer", "license_manager"} {
		if _, err := q.CreateUserDepartmentRole(context.Background(), repository.CreateUserDepartmentRoleParams{
			AppUserID:    alice.ID,
			DepartmentID: &dept.ID,
			Role:         role,
		}); err != nil {
			t.Fatalf("seed CreateUserDepartmentRole(%s): %v", role, err)
		}
	}

	// 連携なし・無効化済みの app_user。
	bob := seedAppUserRow(t, q, "bob_disabled", nil, nil)
	if _, err := q.DisableAppUser(context.Background(), bob.ID); err != nil {
		t.Fatalf("seed DisableAppUser: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/app-users", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	aliceRow := tableRowContaining(t, rec, "alice_linked")
	for _, want := range []string{"local", "山田 太郎", "alice@example.com", "有効", ">2<"} {
		if !strings.Contains(aliceRow, want) {
			t.Errorf("alice row does not contain %q:\n%s", want, aliceRow)
		}
	}
	// ロール管理画面への導線。
	if !strings.Contains(aliceRow, "/admin/roles?app_user_id="+itoa64(alice.ID)) {
		t.Errorf("alice row has no link to /admin/roles:\n%s", aliceRow)
	}

	bobRow := tableRowContaining(t, rec, "bob_disabled")
	for _, want := range []string{"無効", ">-<", ">0<"} {
		if !strings.Contains(bobRow, want) {
			t.Errorf("bob row does not contain %q:\n%s", want, bobRow)
		}
	}
}

// --- 無効化 -----------------------------------------------------------

// 無効化は disabled_at 設定 + そのユーザの既存セッション削除 + audit
// (app_user.disable) を行う。既存 Cookie でのアクセスは /login に戻る。
func TestAdminAppUsers_Disable_SetsDisabledAtAndKillsSession(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// 対象ユーザは viewer としてログイン済み (session あり)。
	victimCookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleViewer)
	victimID := *sessionForCookie(t, store, victimCookie).AppUserID

	// 無効化前は既存 Cookie でアクセスできる。
	req := httptest.NewRequest(http.MethodGet, "/vendors", nil)
	req.AddCookie(victimCookie)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)

	adminCookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, postFormAs(t, store, adminCookie,
		"/admin/app-users/"+itoa64(victimID)+"/disable", nil))
	handlertest.AssertRedirect(t, rec, "/admin/app-users")

	u, err := q.GetAppUser(context.Background(), victimID)
	if err != nil {
		t.Fatalf("GetAppUser after disable: %v", err)
	}
	if u.DisabledAt == nil {
		t.Error("disabled_at is nil, want set")
	}
	if got := countAuditLogs(t, db, "app_user.disable", "app_user", victimID); got != 1 {
		t.Errorf("audit app_user.disable count = %d, want 1", got)
	}

	// セッションは DB から消えている。
	if _, err := store.GetByID(context.Background(), victimCookie.Value); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetByID(victim session) = %v, want sql.ErrNoRows (セッション削除)", err)
	}
	// 既存 Cookie でのアクセスは匿名扱いになり /login に戻される。
	req = httptest.NewRequest(http.MethodGet, "/vendors", nil)
	req.AddCookie(victimCookie)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("Location = %q, want /login prefix", loc)
	}
}

// 自分自身の無効化は 400 (全 system_admin ロックアウトの最短経路を塞ぐ)。
func TestAdminAppUsers_Disable_Self_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	adminCookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)
	adminID := *sessionForCookie(t, store, adminCookie).AppUserID

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postFormAs(t, store, adminCookie,
		"/admin/app-users/"+itoa64(adminID)+"/disable", nil))
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	u, err := q.GetAppUser(context.Background(), adminID)
	if err != nil {
		t.Fatalf("GetAppUser: %v", err)
	}
	if u.DisabledAt != nil {
		t.Error("disabled_at is set, want nil (自分は無効化されない)")
	}
	if got := countAuditLogs(t, db, "app_user.disable", "app_user", adminID); got != 0 {
		t.Errorf("audit app_user.disable count = %d, want 0", got)
	}
}

// 既に無効なユーザの無効化は 409。
func TestAdminAppUsers_Disable_AlreadyDisabled_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "already_off", nil, nil)
	if _, err := q.DisableAppUser(context.Background(), target.ID); err != nil {
		t.Fatalf("seed DisableAppUser: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/disable", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusConflict)
	if got := countAuditLogs(t, db, "app_user.disable", "app_user", target.ID); got != 0 {
		t.Errorf("audit app_user.disable count = %d, want 0", got)
	}
}

// 存在しない id は 404。
func TestAdminAppUsers_Disable_NotFound_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/99999/disable", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// --- 再有効化 ---------------------------------------------------------

// 再有効化は disabled_at を NULL に戻し audit (app_user.enable) を記録する。
func TestAdminAppUsers_Enable(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "to_enable", nil, nil)
	if _, err := q.DisableAppUser(context.Background(), target.ID); err != nil {
		t.Fatalf("seed DisableAppUser: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/enable", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, "/admin/app-users")

	u, err := q.GetAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("GetAppUser after enable: %v", err)
	}
	if u.DisabledAt != nil {
		t.Errorf("disabled_at = %v, want nil", u.DisabledAt)
	}
	if got := countAuditLogs(t, db, "app_user.enable", "app_user", target.ID); got != 1 {
		t.Errorf("audit app_user.enable count = %d, want 1", got)
	}
}

// 有効なユーザの再有効化は 409。
func TestAdminAppUsers_Enable_AlreadyEnabled_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "still_on", nil, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/enable", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusConflict)
	if got := countAuditLogs(t, db, "app_user.enable", "app_user", target.ID); got != 0 {
		t.Errorf("audit app_user.enable count = %d, want 0", got)
	}
}

// --- notify_email 変更 -------------------------------------------------

// notify_email は TrimSpace して保存し、audit
// (app_user.notify_email_change {old,new}) を記録する。
func TestAdminAppUsers_NotifyEmail_Update(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	old := "old@example.com"
	target := seedAppUserRow(t, q, "mail_user", &old, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/notify-email",
		middleware.RoleSystemAdmin, url.Values{"notify_email": {" new@example.com "}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, "/admin/app-users")

	u, err := q.GetAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("GetAppUser after notify-email: %v", err)
	}
	if u.NotifyEmail == nil || *u.NotifyEmail != "new@example.com" {
		t.Errorf("notify_email = %v, want new@example.com (trim 済み)", u.NotifyEmail)
	}

	diff := auditDiff(t, db, "app_user.notify_email_change", "app_user", target.ID)
	if got := diff["old"]; got != "old@example.com" {
		t.Errorf("diff old = %v, want old@example.com", got)
	}
	if got := diff["new"]; got != "new@example.com" {
		t.Errorf("diff new = %v, want new@example.com", got)
	}
}

// 空入力は NULL 化 (通知先なし)。diff の new は省略する。
func TestAdminAppUsers_NotifyEmail_EmptyClearsToNull(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	old := "clear@example.com"
	target := seedAppUserRow(t, q, "mail_clear", &old, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/notify-email",
		middleware.RoleSystemAdmin, url.Values{"notify_email": {"  "}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, "/admin/app-users")

	u, err := q.GetAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("GetAppUser after clear: %v", err)
	}
	if u.NotifyEmail != nil {
		t.Errorf("notify_email = %q, want NULL", *u.NotifyEmail)
	}

	diff := auditDiff(t, db, "app_user.notify_email_change", "app_user", target.ID)
	if got := diff["old"]; got != "clear@example.com" {
		t.Errorf("diff old = %v, want clear@example.com", got)
	}
	if got, ok := diff["new"]; ok {
		t.Errorf("diff new = %v, want omitted (空 = NULL)", got)
	}
}

// "@" を含まない非空入力は 400 で不変 (軽量検証、本格検証は通知フェーズ)。
func TestAdminAppUsers_NotifyEmail_Invalid_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	old := "keep@example.com"
	target := seedAppUserRow(t, q, "mail_bad", &old, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/"+itoa64(target.ID)+"/notify-email",
		middleware.RoleSystemAdmin, url.Values{"notify_email": {"not-an-email"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	u, err := q.GetAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("GetAppUser after invalid: %v", err)
	}
	if u.NotifyEmail == nil || *u.NotifyEmail != "keep@example.com" {
		t.Errorf("notify_email = %v, want keep@example.com (不変)", u.NotifyEmail)
	}
	if got := countAuditLogs(t, db, "app_user.notify_email_change", "app_user", target.ID); got != 0 {
		t.Errorf("audit notify_email_change count = %d, want 0", got)
	}
}

// 存在しない id への notify-email は 404。
func TestAdminAppUsers_NotifyEmail_NotFound_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		"/admin/app-users/99999/notify-email",
		middleware.RoleSystemAdmin, url.Values{"notify_email": {"x@example.com"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}
