package web_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// approvals_test.go は承認管理 3 画面 (仕様 §6.1 / Plan approvals.md) の
// web 層テスト。
//
//   - GET  /approvals                          部署選択 + 製品 × 承認状態一覧
//   - GET  /approvals/{deptID}/{productID}     登録・編集画面 (履歴 + フォーム)
//   - POST /approvals/{deptID}/{productID}     登録 (既存アクティブは 409)
//   - POST /approvals/{deptID}/{productID}/revoke  取消 (revoke_reason 必須)
//   - GET/POST /admin/global-approvals         全社設定 (system_admin のみ)
//
// 認可は dept_security_admin 以上 (viewer / license_manager は 403)。
// audit_logs へ approval.grant / approval.revoke /
// product.default_approval_change が記録されることも確認する。

// seedApprovalDept は承認テスト用の現役部署を 1 つ作る。
func seedApprovalDept(t *testing.T, q *repository.Queries) repository.Department {
	t.Helper()
	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEV1",
		Name: "開発部",
	})
	if err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}
	return d
}

// seedApprovalVendor は承認テスト用のベンダーを 1 つ作る。
func seedApprovalVendor(t *testing.T, q *repository.Queries) repository.Vendor {
	t.Helper()
	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{
		Name: "TestVendor",
	})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}
	return v
}

// seedApprovalProduct は default_approval_status を指定して製品を作る。
func seedApprovalProduct(t *testing.T, q *repository.Queries, vendorID int64, name, defaultStatus string) repository.Product {
	t.Helper()
	p, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID:              vendorID,
		CanonicalName:         name,
		SoftwareType:          "desktop",
		DefaultApprovalStatus: defaultStatus,
	})
	if err != nil {
		t.Fatalf("seed CreateProduct(%s): %v", name, err)
	}
	return p
}

// seedActiveApproval はアクティブな承認レコードを直接 INSERT する。
func seedActiveApproval(t *testing.T, q *repository.Queries, deptID, productID int64, status, scopeType string, expiresAt *time.Time) repository.DepartmentProductApproval {
	t.Helper()
	conditions := "社内利用のみ"
	var condPtr *string
	if status == "conditional" {
		condPtr = &conditions
	}
	a, err := q.CreateApproval(context.Background(), repository.CreateApprovalParams{
		DepartmentID: deptID,
		ProductID:    productID,
		Status:       status,
		ScopeType:    scopeType,
		Conditions:   condPtr,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatalf("seed CreateApproval: %v", err)
	}
	return a
}

// countAuditLogs は audit_logs の (action, entity_type, entity_id) 一致行数。
func countAuditLogs(t *testing.T, db *sql.DB, action, entityType string, entityID int64) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_logs WHERE action = ? AND entity_type = ? AND entity_id = ?`,
		action, entityType, entityID).Scan(&n); err != nil {
		t.Fatalf("count audit_logs: %v", err)
	}
	return n
}

// auditDiff は (action, entity_type, entity_id) 一致行の diff_json を JSON
// パースして返す。NULL・不正 JSON は fail (Plan「diff_json に主要フィールド」)。
func auditDiff(t *testing.T, db *sql.DB, action, entityType string, entityID int64) map[string]any {
	t.Helper()
	var diff sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT diff_json FROM audit_logs WHERE action = ? AND entity_type = ? AND entity_id = ?`,
		action, entityType, entityID).Scan(&diff); err != nil {
		t.Fatalf("select diff_json for %s %s/%d: %v", action, entityType, entityID, err)
	}
	if !diff.Valid {
		t.Fatalf("diff_json for %s %s/%d is NULL, want JSON with main fields", action, entityType, entityID)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(diff.String), &m); err != nil {
		t.Fatalf("diff_json for %s %s/%d is not valid JSON: %v (raw: %s)",
			action, entityType, entityID, err, diff.String)
	}
	return m
}

func approvalDetailPath(deptID, productID int64) string {
	return "/approvals/" + itoa64(deptID) + "/" + itoa64(productID)
}

// itoa64 はテスト用の int64 → string (10 進)。
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- 認可 -------------------------------------------------------------

// /approvals は dept_security_admin 以上 (仕様 §6.1)。viewer はもちろん、
// license_manager もロール階層で dept_security_admin より下位のため 403。
func TestApprovals_List_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	cases := []struct {
		role middleware.Role
		want int
	}{
		{middleware.RoleGeneralUser, http.StatusForbidden},
		{middleware.RoleViewer, http.StatusForbidden},
		{middleware.RoleLicenseManager, http.StatusForbidden},
		{middleware.RoleDepartmentSecurityAdmin, http.StatusOK},
		{middleware.RoleSystemAdmin, http.StatusOK},
	}
	for _, tc := range cases {
		req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/approvals", tc.role, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("GET /approvals as %s: status = %d, want %d", tc.role, rec.Code, tc.want)
		}
	}
}

// /admin/global-approvals は system_admin のみ。
func TestGlobalApprovals_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "AdminTool", "department_discretion")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/admin/global-approvals", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store, "/admin/global-approvals/"+itoa64(p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{"default_approval_status": {"globally_approved"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/admin/global-approvals", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "AdminTool")
}

// --- 一覧 -------------------------------------------------------------

// 部署未選択の /approvals は現役部署の選択フォームを出す。
func TestApprovals_List_ShowsDepartmentSelect(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/approvals", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="department_id"`)
	handlertest.AssertContains(t, rec, d.Name)
}

// 部署選択後は全製品について Evaluate の Verdict を日本語で表示する。
// specific_* スコープは一覧では InScope を評価しない (部署単位表示) ため
// 未承認と出るが、行に scope_type を注記する。
func TestApprovals_List_ShowsVerdictsInJapanese(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)

	seedApprovalProduct(t, q, v.ID, "GAApp", "globally_approved")        // 許可
	seedApprovalProduct(t, q, v.ID, "GPApp", "globally_prohibited")      // 禁止
	seedApprovalProduct(t, q, v.ID, "UnknownApp", "unknown")             // 未審査
	seedApprovalProduct(t, q, v.ID, "NoRecApp", "department_discretion") // 未承認

	pCond := seedApprovalProduct(t, q, v.ID, "CondApp", "department_discretion") // 条件付き
	seedActiveApproval(t, q, d.ID, pCond.ID, "conditional", "department_wide", nil)

	past := time.Now().Add(-24 * time.Hour)
	pExp := seedApprovalProduct(t, q, v.ID, "ExpApp", "department_discretion") // 期限切れ
	seedActiveApproval(t, q, d.ID, pExp.ID, "approved", "department_wide", &past)

	pScope := seedApprovalProduct(t, q, v.ID, "ScopeApp", "department_discretion")
	seedActiveApproval(t, q, d.ID, pScope.ID, "approved", "specific_users", nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/approvals?department_id="+itoa64(d.ID), middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	for _, want := range []string{
		"GAApp", "GPApp", "UnknownApp", "NoRecApp", "CondApp", "ExpApp", "ScopeApp",
		"許可", "禁止", "未審査", "未承認", "条件付き", "期限切れ",
		"specific_users", // scope_type の注記
	} {
		handlertest.AssertContains(t, rec, want)
	}
}

// --- 登録・編集画面 -----------------------------------------------------

func TestApprovals_Show_FormWithoutActiveApproval(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "FormApp", "department_discretion")

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, approvalDetailPath(d.ID, p.ID), middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="status"`)
	handlertest.AssertContains(t, rec, `name="expires_at"`)
	handlertest.AssertContains(t, rec, "FormApp")
}

func TestApprovals_Show_UnknownProduct_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, approvalDetailPath(d.ID, 999999), middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// --- 登録 -------------------------------------------------------------

func TestApprovals_Create_Success_RecordsRowAndAudit(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "GrantApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{
			"status":     {"approved"},
			"expires_at": {"2030-12-31"},
			"note":       {"年次レビュー済み"},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, approvalDetailPath(d.ID, p.ID))

	row, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval after create: %v", err)
	}
	if row.Status != "approved" {
		t.Errorf("status = %q, want approved", row.Status)
	}
	if row.ScopeType != "department_wide" {
		t.Errorf("scope_type = %q, want department_wide (MVP は固定)", row.ScopeType)
	}
	if row.ApprovedByAppUserID == nil {
		t.Error("approved_by_app_user_id is nil, want session AppUserID")
	}
	if row.ExpiresAt == nil {
		t.Error("expires_at is nil, want 2030-12-31")
	}
	if got := countAuditLogs(t, db, "approval.grant", "department_product_approval", row.ID); got != 1 {
		t.Errorf("audit approval.grant count = %d, want 1", got)
	}

	// diff_json に主要フィールド (Plan approvals.md)。conditions は
	// 未入力なので省略される。
	diff := auditDiff(t, db, "approval.grant", "department_product_approval", row.ID)
	if got := diff["status"]; got != "approved" {
		t.Errorf("grant diff status = %v, want approved", got)
	}
	if got := diff["scope_type"]; got != "department_wide" {
		t.Errorf("grant diff scope_type = %v, want department_wide", got)
	}
	if got := diff["expires_at"]; got != "2030-12-31" {
		t.Errorf("grant diff expires_at = %v, want 2030-12-31", got)
	}
	if got, ok := diff["conditions"]; ok {
		t.Errorf("grant diff conditions = %v, want omitted (未入力)", got)
	}
}

// 既存アクティブがある場合の登録は 409 (取消して再登録の変更方式)。
func TestApprovals_Create_ConflictWhenActiveExists(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "DupApp", "department_discretion")
	seedActiveApproval(t, q, d.ID, p.ID, "approved", "department_wide", nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{"status": {"prohibited"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusConflict)
	handlertest.AssertContains(t, rec, "取消")

	// アクティブ行は元のまま 1 件。
	row, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval: %v", err)
	}
	if row.Status != "approved" {
		t.Errorf("active status = %q, want approved (未変更)", row.Status)
	}
}

func TestApprovals_Create_ConditionalRequiresConditions(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "CondReqApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{"status": {"conditional"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusBadRequest)
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveApproval = %v, want sql.ErrNoRows (登録されない)", err)
	}
}

func TestApprovals_Create_InvalidExpiresAt_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "BadDateApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{
			"status":     {"approved"},
			"expires_at": {"not-a-date"},
		})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusBadRequest)
}

// --- 取消 → 再登録 ------------------------------------------------------

func TestApprovals_Revoke_ReasonRequired(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "KeepApp", "department_discretion")
	seedActiveApproval(t, q, d.ID, p.ID, "approved", "department_wide", nil)

	// 空白のみの理由は空と同じ扱いで 400。
	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID)+"/revoke",
		middleware.RoleDepartmentSecurityAdmin, url.Values{"revoke_reason": {"   "}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusBadRequest)
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	}); err != nil {
		t.Fatalf("GetActiveApproval = %v, want active row to survive", err)
	}
}

func TestApprovals_Revoke_NoActiveApproval_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "NoneApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID)+"/revoke",
		middleware.RoleDepartmentSecurityAdmin, url.Values{"revoke_reason": {"誤登録"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// 取消 → 履歴に残る → 同じ (部署, 製品) に再登録できる。
func TestApprovals_RevokeThenRecreate(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	d := seedApprovalDept(t, q)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "CycleApp", "department_discretion")
	old := seedActiveApproval(t, q, d.ID, p.ID, "approved", "department_wide", nil)

	// 取消。
	req := handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID)+"/revoke",
		middleware.RoleDepartmentSecurityAdmin, url.Values{"revoke_reason": {"部署方針変更"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, approvalDetailPath(d.ID, p.ID))

	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveApproval after revoke = %v, want sql.ErrNoRows", err)
	}
	if got := countAuditLogs(t, db, "approval.revoke", "department_product_approval", old.ID); got != 1 {
		t.Errorf("audit approval.revoke count = %d, want 1", got)
	}
	// revoke の diff_json は理由のみ (revoked_by は app_user_id カラム、
	// revoked_at は occurred_at が持つため冗長に含めない)。
	revokeDiff := auditDiff(t, db, "approval.revoke", "department_product_approval", old.ID)
	if got := revokeDiff["revoke_reason"]; got != "部署方針変更" {
		t.Errorf("revoke diff revoke_reason = %v, want 部署方針変更", got)
	}

	// 履歴 (取消済み行 + 理由) が登録・編集画面に残る。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, approvalDetailPath(d.ID, p.ID), middleware.RoleDepartmentSecurityAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "部署方針変更")

	// 再登録は成功し、新しい行が作られる。
	req = handlertest.AuthenticatedPostForm(t, db, store, approvalDetailPath(d.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, url.Values{
			"status":     {"conditional"},
			"conditions": {"検証環境のみで利用"},
		})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, approvalDetailPath(d.ID, p.ID))

	row, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: d.ID,
		ProductID:    p.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval after recreate: %v", err)
	}
	if row.ID == old.ID {
		t.Error("recreate reused the old row; want a new row (取消 + 新規)")
	}
	if row.Status != "conditional" {
		t.Errorf("status = %q, want conditional", row.Status)
	}
	// conditional の grant diff には conditions が含まれる (expires_at は
	// 未入力なので省略)。
	grantDiff := auditDiff(t, db, "approval.grant", "department_product_approval", row.ID)
	if got := grantDiff["status"]; got != "conditional" {
		t.Errorf("grant diff status = %v, want conditional", got)
	}
	if got := grantDiff["conditions"]; got != "検証環境のみで利用" {
		t.Errorf("grant diff conditions = %v, want 検証環境のみで利用", got)
	}
	if got, ok := grantDiff["expires_at"]; ok {
		t.Errorf("grant diff expires_at = %v, want omitted (未入力)", got)
	}
}

// --- 全社設定 -----------------------------------------------------------

func TestGlobalApprovals_Update_RecordsChangeAndAudit(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "GlobalApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/global-approvals/"+itoa64(p.ID),
		middleware.RoleSystemAdmin, url.Values{"default_approval_status": {"globally_prohibited"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/admin/global-approvals")

	got, err := q.GetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got.DefaultApprovalStatus != "globally_prohibited" {
		t.Errorf("default_approval_status = %q, want globally_prohibited", got.DefaultApprovalStatus)
	}
	if n := countAuditLogs(t, db, "product.default_approval_change", "product", p.ID); n != 1 {
		t.Errorf("audit product.default_approval_change count = %d, want 1", n)
	}
	// diff_json は old / new (product_id は entity_id が持つ)。
	diff := auditDiff(t, db, "product.default_approval_change", "product", p.ID)
	if got := diff["old"]; got != "department_discretion" {
		t.Errorf("global diff old = %v, want department_discretion", got)
	}
	if got := diff["new"]; got != "globally_prohibited" {
		t.Errorf("global diff new = %v, want globally_prohibited", got)
	}
}

func TestGlobalApprovals_Update_InvalidValue_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "BogusApp", "department_discretion")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/global-approvals/"+itoa64(p.ID),
		middleware.RoleSystemAdmin, url.Values{"default_approval_status": {"bogus"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	got, err := q.GetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got.DefaultApprovalStatus != "department_discretion" {
		t.Errorf("default_approval_status = %q, want department_discretion (未変更)", got.DefaultApprovalStatus)
	}
}

func TestGlobalApprovals_Update_UnknownProduct_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/global-approvals/999999",
		middleware.RoleSystemAdmin, url.Values{"default_approval_status": {"globally_approved"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// TestApprovals_RetiredDepartment_404 は廃止済み部署 (valid_to NOT NULL) への
// 直接 URL アクセスで承認の閲覧・登録・取消ができないことを確認する
// (一覧の「現役部署のみ選択可」を直接 URL でも貫く)。
func TestApprovals_RetiredDepartment_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	dept := seedApprovalDept(t, q)
	vendor := seedApprovalVendor(t, q)
	product := seedApprovalProduct(t, q, vendor.ID, "RetiredDeptApp", "department_discretion")
	if _, err := db.Exec(`UPDATE departments SET valid_to = date('now') WHERE id = ?`, dept.ID); err != nil {
		t.Fatalf("retire department: %v", err)
	}

	target := fmt.Sprintf("/approvals/%d/%d", dept.ID, product.ID)
	getReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, target, middleware.RoleDepartmentSecurityAdmin, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	handlertest.AssertStatus(t, getRec, http.StatusNotFound)

	form := url.Values{"status": {"approved"}}
	postReq := handlertest.AuthenticatedPostForm(t, db, store, target, middleware.RoleDepartmentSecurityAdmin, form)
	postRec := httptest.NewRecorder()
	r.ServeHTTP(postRec, postReq)
	handlertest.AssertStatus(t, postRec, http.StatusNotFound)
}
