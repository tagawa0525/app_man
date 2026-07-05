package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// roles_test.go は /admin/roles (仕様 §6.1 / Plan admin-app-users-roles.md)
// の web 層テスト。
//
//   - GET  /admin/roles?app_user_id=              app_user 選択 + アクティブ
//     ロール一覧 (role / 部署名 (NULL=全社) / granted_at) + 付与フォーム
//   - POST /admin/roles/{appUserID}               付与 (アクティブ重複 409)
//   - POST /admin/roles/{appUserID}/{roleID}/revoke  剥奪 (revoked_at 設定)
//
// 認可は system_admin のみ。付与の検証は create-app-user CLI と同じ規則:
// role は AllRoles のみ、system_admin は department NULL 強制、他ロールは
// 現役部署必須 (無し・廃止 400)。剥奪のロックアウト防止は 2 層:
// 自分の system_admin ロール剥奪 400 と、「アクティブ system_admin ロール
// を持つ有効 (disabled_at IS NULL) ユーザ」が 1 人だけのときの
// system_admin ロール剥奪 400 (無効化ユーザは勘定から除外、Plan)。

// seedRoleFor は user_department_roles に直接アクティブ行を作る
// (付与ハンドラを経由しない seed 用)。
func seedRoleFor(t *testing.T, q *repository.Queries, appUserID int64, departmentID *int64, role string) repository.UserDepartmentRole {
	t.Helper()
	row, err := q.CreateUserDepartmentRole(context.Background(), repository.CreateUserDepartmentRoleParams{
		AppUserID:    appUserID,
		DepartmentID: departmentID,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("seed CreateUserDepartmentRole(%s): %v", role, err)
	}
	return row
}

// activeRoleID は (app_user_id, role) のアクティブ行 id を返す
// (自分の system_admin ロール行の特定などに使う)。
func activeRoleID(t *testing.T, db *sql.DB, appUserID int64, role string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT id FROM user_department_roles
		 WHERE app_user_id = ? AND role = ? AND revoked_at IS NULL`,
		appUserID, role).Scan(&id); err != nil {
		t.Fatalf("activeRoleID(%d, %s): %v", appUserID, role, err)
	}
	return id
}

// rolesPath は GET 用の一覧 URL。
func rolesPath(appUserID int64) string {
	return "/admin/roles?app_user_id=" + itoa64(appUserID)
}

// grantPath / revokePath は POST 用 URL。
func grantPath(appUserID int64) string {
	return "/admin/roles/" + itoa64(appUserID)
}

func revokePath(appUserID, roleID int64) string {
	return "/admin/roles/" + itoa64(appUserID) + "/" + itoa64(roleID) + "/revoke"
}

// --- 認可 -------------------------------------------------------------

// /admin/roles は system_admin のみ (仕様 §6.1)。
func TestAdminRoles_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "roles_authz", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, rolesPath(target.ID), middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{"role": {"viewer"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/roles", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- 一覧 -------------------------------------------------------------

// app_user を選ぶとアクティブロール一覧 (role / 部署名 / granted_at) と
// 付与フォーム (AllRoles + 現役部署 + 全社) を表示する。
func TestAdminRoles_List_ShowsActiveRoles(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "roles_target", nil, nil)
	dept := seedApprovalDept(t, q)
	viewerRole := seedRoleFor(t, q, target.ID, &dept.ID, "viewer")
	adminRole := seedRoleFor(t, q, target.ID, nil, "system_admin")

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, rolesPath(target.ID), middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "roles_target")
	handlertest.AssertContains(t, rec, "viewer")
	handlertest.AssertContains(t, rec, "開発部")
	// department_id NULL は「全社」表示。
	handlertest.AssertContains(t, rec, "全社")
	// 各アクティブロール行には剥奪ボタン (POST 先) が付く。
	handlertest.AssertContains(t, rec, revokePath(target.ID, viewerRole.ID))
	handlertest.AssertContains(t, rec, revokePath(target.ID, adminRole.ID))
	// 付与フォームの role select は AllRoles (general_user はロール行に
	// 無いので select 由来でのみ現れる)。
	handlertest.AssertContains(t, rec, "general_user")
	// 付与フォームの POST 先。
	handlertest.AssertContains(t, rec, grantPath(target.ID))
}

// app_user_id 未指定はユーザ選択のみ表示 (200)。不正・不存在は 404。
func TestAdminRoles_List_Selection(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "roles_select", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/roles", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "roles_select")
	_ = target

	for _, raw := range []string{"abc", "0", "99999"} {
		req = handlertest.AuthenticatedRequest(t, db, store,
			http.MethodGet, "/admin/roles?app_user_id="+raw, middleware.RoleSystemAdmin, nil)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET app_user_id=%q: status = %d, want 404", raw, rec.Code)
		}
	}
}

// --- 付与 -------------------------------------------------------------

// 部署ロールの付与はアクティブ行 INSERT + audit role.grant
// {role, department_id}。
func TestAdminRoles_Grant(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_target", nil, nil)
	dept := seedApprovalDept(t, q)

	req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{
			"role":          {"viewer"},
			"department_id": {itoa64(dept.ID)},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, rolesPath(target.ID))

	rows, err := q.ListActiveRolesWithDepartmentForAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ListActiveRolesWithDepartmentForAppUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active roles = %d, want 1", len(rows))
	}
	if rows[0].Role != "viewer" || rows[0].DepartmentID == nil || *rows[0].DepartmentID != dept.ID {
		t.Errorf("role row = %+v, want viewer / dept %d", rows[0], dept.ID)
	}

	diff := auditDiff(t, db, "role.grant", "user_department_role", rows[0].ID)
	if got := diff["role"]; got != "viewer" {
		t.Errorf("grant diff role = %v, want viewer", got)
	}
	if got := diff["department_id"]; got != float64(dept.ID) {
		t.Errorf("grant diff department_id = %v, want %d", got, dept.ID)
	}
}

// system_admin の付与は department NULL 固定で成功し、diff の
// department_id は null になる。
func TestAdminRoles_Grant_SystemAdmin_GlobalScope(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_admin", nil, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{
			"role":          {"system_admin"},
			"department_id": {""},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, rolesPath(target.ID))

	rows, err := q.ListActiveRolesWithDepartmentForAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ListActiveRolesWithDepartmentForAppUser: %v", err)
	}
	if len(rows) != 1 || rows[0].Role != "system_admin" || rows[0].DepartmentID != nil {
		t.Fatalf("role rows = %+v, want single system_admin with NULL department", rows)
	}
	diff := auditDiff(t, db, "role.grant", "user_department_role", rows[0].ID)
	if got, ok := diff["department_id"]; !ok || got != nil {
		t.Errorf("grant diff department_id = %v (present=%t), want null", got, ok)
	}
}

// アクティブ重複は 409 (部署スコープ・全社スコープの両方)。
func TestAdminRoles_Grant_Duplicate_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_dup", nil, nil)
	dept := seedApprovalDept(t, q)
	seedRoleFor(t, q, target.ID, &dept.ID, "viewer")
	seedRoleFor(t, q, target.ID, nil, "system_admin")

	req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{
			"role":          {"viewer"},
			"department_id": {itoa64(dept.ID)},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusConflict)

	req = handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{"role": {"system_admin"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusConflict)

	rows, err := q.ListActiveRolesWithDepartmentForAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ListActiveRolesWithDepartmentForAppUser: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("active roles = %d, want 2 (重複は追加されない)", len(rows))
	}
}

// role の検証: AllRoles 以外は 400。
func TestAdminRoles_Grant_InvalidRole_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_badrole", nil, nil)
	dept := seedApprovalDept(t, q)

	for _, bad := range []string{"", "superuser", "SYSTEM_ADMIN"} {
		req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
			middleware.RoleSystemAdmin, url.Values{
				"role":          {bad},
				"department_id": {itoa64(dept.ID)},
			})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST role=%q: status = %d, want 400", bad, rec.Code)
		}
	}
}

// system_admin 以外は現役部署必須: 未指定・不存在・廃止部署は 400。
func TestAdminRoles_Grant_DepartmentValidation_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_baddept", nil, nil)
	retired, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "OLD1",
		Name: "廃止部署",
	})
	if err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}
	if _, err := q.SoftDeleteDepartment(context.Background(), retired.ID); err != nil {
		t.Fatalf("seed SoftDeleteDepartment: %v", err)
	}

	for _, deptValue := range []string{"", "99999", itoa64(retired.ID)} {
		req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
			middleware.RoleSystemAdmin, url.Values{
				"role":          {"viewer"},
				"department_id": {deptValue},
			})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST department_id=%q: status = %d, want 400", deptValue, rec.Code)
		}
	}
	rows, err := q.ListActiveRolesWithDepartmentForAppUser(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ListActiveRolesWithDepartmentForAppUser: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("active roles = %d, want 0", len(rows))
	}
}

// system_admin への部署指定は 400 (全社スコープ固定)。
func TestAdminRoles_Grant_SystemAdminWithDept_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "grant_admindept", nil, nil)
	dept := seedApprovalDept(t, q)

	req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{
			"role":          {"system_admin"},
			"department_id": {itoa64(dept.ID)},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)
}

// 不存在 app_user への付与は 404。
func TestAdminRoles_Grant_UserNotFound_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, grantPath(99999),
		middleware.RoleSystemAdmin, url.Values{"role": {"system_admin"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// --- 剥奪 -------------------------------------------------------------

// 剥奪は revoked_at 設定 + audit role.revoke。同じ (role, 部署) を再付与
// できる (部分 UNIQUE はアクティブ行のみ対象)。
func TestAdminRoles_Revoke_ThenRegrant(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "revoke_target", nil, nil)
	dept := seedApprovalDept(t, q)
	role := seedRoleFor(t, q, target.ID, &dept.ID, "viewer")

	req := handlertest.AuthenticatedPostForm(t, db, store, revokePath(target.ID, role.ID),
		middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, rolesPath(target.ID))

	row, err := q.GetUserDepartmentRole(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("GetUserDepartmentRole after revoke: %v", err)
	}
	if row.RevokedAt == nil {
		t.Error("revoked_at is nil, want set")
	}
	diff := auditDiff(t, db, "role.revoke", "user_department_role", role.ID)
	if got := diff["role"]; got != "viewer" {
		t.Errorf("revoke diff role = %v, want viewer", got)
	}
	if got := diff["department_id"]; got != float64(dept.ID) {
		t.Errorf("revoke diff department_id = %v, want %d", got, dept.ID)
	}

	// 一覧から剥奪済みロールの行 (剥奪ボタン) が消える。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, rolesPath(target.ID), middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	assertNotContains(t, rec, revokePath(target.ID, role.ID))

	// 再付与できる。
	req = handlertest.AuthenticatedPostForm(t, db, store, grantPath(target.ID),
		middleware.RoleSystemAdmin, url.Values{
			"role":          {"viewer"},
			"department_id": {itoa64(dept.ID)},
		})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, rolesPath(target.ID))
}

// 不存在 roleID・他ユーザの roleID・剥奪済み roleID は 404。
func TestAdminRoles_Revoke_NotFound_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	target := seedAppUserRow(t, q, "revoke_404", nil, nil)
	other := seedAppUserRow(t, q, "revoke_404_other", nil, nil)
	dept := seedApprovalDept(t, q)
	otherRole := seedRoleFor(t, q, other.ID, &dept.ID, "viewer")
	revoked := seedRoleFor(t, q, target.ID, &dept.ID, "license_manager")
	if _, err := q.RevokeUserDepartmentRole(context.Background(), repository.RevokeUserDepartmentRoleParams{
		ID:        revoked.ID,
		AppUserID: target.ID,
	}); err != nil {
		t.Fatalf("seed RevokeUserDepartmentRole: %v", err)
	}

	for name, path := range map[string]string{
		"nonexistent":     revokePath(target.ID, 99999),
		"other user role": revokePath(target.ID, otherRole.ID),
		"already revoked": revokePath(target.ID, revoked.ID),
	} {
		req := handlertest.AuthenticatedPostForm(t, db, store, path,
			middleware.RoleSystemAdmin, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
	}
}

// 自分の system_admin ロールは剥奪できない (400)。他に有効な
// system_admin がいても不可 (誤操作での自己降格を塞ぐ)。
func TestAdminRoles_Revoke_OwnSystemAdmin_400(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	adminCookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)
	adminID := *sessionForCookie(t, store, adminCookie).AppUserID
	// 別の有効な system_admin がいる状態でも自分のロールは剥奪不可。
	handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)

	ownRoleID := activeRoleID(t, db, adminID, "system_admin")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postFormAs(t, store, adminCookie, revokePath(adminID, ownRoleID), nil))
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	if got := countAuditLogs(t, db, "role.revoke", "user_department_role", ownRoleID); got != 0 {
		t.Errorf("audit role.revoke count = %d, want 0", got)
	}
}

// 「アクティブ system_admin ロールを持つ有効ユーザ」が 1 人だけなら
// system_admin ロールは剥奪できない (400)。無効化ユーザは勘定から除外
// する。別の有効な admin を追加すれば剥奪できる。
func TestAdminRoles_Revoke_LastSystemAdmin_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	adminCookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)

	// 無効化済みユーザ B にも system_admin ロールがあるが、勘定から
	// 除外されるため「有効な admin」は操作者 1 人だけ。
	frozen := seedAppUserRow(t, q, "frozen_admin", nil, nil)
	frozenRole := seedRoleFor(t, q, frozen.ID, nil, "system_admin")
	if _, err := q.DisableAppUser(context.Background(), frozen.ID); err != nil {
		t.Fatalf("seed DisableAppUser: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postFormAs(t, store, adminCookie, revokePath(frozen.ID, frozenRole.ID), nil))
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	row, err := q.GetUserDepartmentRole(context.Background(), frozenRole.ID)
	if err != nil {
		t.Fatalf("GetUserDepartmentRole: %v", err)
	}
	if row.RevokedAt != nil {
		t.Error("revoked_at is set, want nil (最後の有効 admin 状態では剥奪不可)")
	}

	// 有効な system_admin をもう 1 人足せば剥奪できる。
	second := seedAppUserRow(t, q, "second_admin", nil, nil)
	seedRoleFor(t, q, second.ID, nil, "system_admin")

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, postFormAs(t, store, adminCookie, revokePath(frozen.ID, frozenRole.ID), nil))
	handlertest.AssertRedirect(t, rec, rolesPath(frozen.ID))

	row, err = q.GetUserDepartmentRole(context.Background(), frozenRole.ID)
	if err != nil {
		t.Fatalf("GetUserDepartmentRole after regrantable revoke: %v", err)
	}
	if row.RevokedAt == nil {
		t.Error("revoked_at is nil, want set (別 admin 追加後は剥奪可)")
	}
	if got := countAuditLogs(t, db, "role.revoke", "user_department_role", frozenRole.ID); got != 1 {
		t.Errorf("audit role.revoke count = %d, want 1", got)
	}
}
