package web_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// departments_migrate_test.go は部署改廃画面 (仕様 §5.15 / Plan
// department-migrate.md) の web 層テスト。
//
//   - GET  /admin/departments/migrate   廃止部署選択 → 移管プレビュー
//   - POST /admin/departments/migrate   1 tx で licenses 移管 + 承認コピー + audit
//
// 認可は system_admin のみ。licenses の slug 衝突行と後継に既存アクティブ
// 承認がある product はスキップして件数報告する (残置は意図的で、運用者の
// 手動対応 → 再実行が正規フロー)。

// seedMigrateDept は部署を 1 つ作る。retired=true なら SoftDelete で
// 廃止済み (valid_to NOT NULL) にして返す。
func seedMigrateDept(t *testing.T, q *repository.Queries, code, name string, retired bool) repository.Department {
	t.Helper()
	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: code,
		Name: name,
	})
	if err != nil {
		t.Fatalf("seed CreateDepartment(%s): %v", code, err)
	}
	if !retired {
		return d
	}
	if _, err := q.SoftDeleteDepartment(context.Background(), d.ID); err != nil {
		t.Fatalf("seed SoftDeleteDepartment(%s): %v", code, err)
	}
	d, err = q.GetDepartment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("seed GetDepartment(%s) after retire: %v", code, err)
	}
	return d
}

// seedMigrateLicense は指定部署所管のライセンスを 1 件 INSERT する。
func seedMigrateLicense(t *testing.T, q *repository.Queries, productID, deptID int64, slug string) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        slug,
		DisplayName:        "移管テスト " + slug,
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          "licenses/seed/seed/" + slug,
	})
	if err != nil {
		t.Fatalf("seed CreateLicense(%s): %v", slug, err)
	}
	return lic
}

// migrateAuditDiffs は action=department.migrate の audit 行の diff_json を
// id 順にパースして返す (再実行テストは複数行を見るため auditDiff の単一行
// 版では足りない)。
func migrateAuditDiffs(t *testing.T, db *sql.DB, entityID int64) []map[string]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT diff_json FROM audit_logs
		 WHERE action = 'department.migrate' AND entity_type = 'department' AND entity_id = ?
		 ORDER BY id`, entityID)
	if err != nil {
		t.Fatalf("select audit_logs for department.migrate: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var diffs []map[string]any
	for rows.Next() {
		var diff sql.NullString
		if err := rows.Scan(&diff); err != nil {
			t.Fatalf("scan diff_json for department.migrate: %v", err)
		}
		if !diff.Valid {
			t.Fatal("diff_json for department.migrate is NULL, want JSON with counts")
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(diff.String), &m); err != nil {
			t.Fatalf("diff_json for department.migrate is not valid JSON: %v (raw: %s)", err, diff.String)
		}
		diffs = append(diffs, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_logs for department.migrate: %v", err)
	}
	return diffs
}

// assertMigrateDiff は diff_json の from / to / 件数 6 フィールドを確認する。
func assertMigrateDiff(t *testing.T, diff map[string]any, fromID, toID int64, moved, licSkipped, copied, apprSkipped float64) {
	t.Helper()
	if got := diff["from"]; got != float64(fromID) {
		t.Errorf("diff from = %v, want %d", got, fromID)
	}
	if got := diff["to"]; got != float64(toID) {
		t.Errorf("diff to = %v, want %d", got, toID)
	}
	if got := diff["licenses_moved"]; got != moved {
		t.Errorf("diff licenses_moved = %v, want %v", got, moved)
	}
	if got := diff["licenses_skipped"]; got != licSkipped {
		t.Errorf("diff licenses_skipped = %v, want %v", got, licSkipped)
	}
	if got := diff["approvals_copied"]; got != copied {
		t.Errorf("diff approvals_copied = %v, want %v", got, copied)
	}
	if got := diff["approvals_skipped"]; got != apprSkipped {
		t.Errorf("diff approvals_skipped = %v, want %v", got, apprSkipped)
	}
}

// migrateForm は POST /admin/departments/migrate のフォーム値。
func migrateForm(fromID, toID int64) url.Values {
	return url.Values{
		"from": {strconv.FormatInt(fromID, 10)},
		"to":   {strconv.FormatInt(toID, 10)},
	}
}

// licenseOwner はライセンスの現在の所管部署 id。
func licenseOwner(t *testing.T, q *repository.Queries, licenseID int64) int64 {
	t.Helper()
	lic, err := q.GetLicenseByID(context.Background(), licenseID)
	if err != nil {
		t.Fatalf("GetLicenseByID(%d): %v", licenseID, err)
	}
	return lic.OwningDepartmentID
}

// --- 認可 -------------------------------------------------------------

// /admin/departments/migrate は system_admin のみ (仕様 §6.1 の /admin/*)。
func TestDepartmentMigrate_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	from := seedMigrateDept(t, q, "MIG-AUTH-F", "旧総務部", true)
	to := seedMigrateDept(t, q, "MIG-AUTH-T", "総務部", false)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostForm(t, db, store, "/admin/departments/migrate",
		middleware.RoleDepartmentSecurityAdmin, migrateForm(from.ID, to.ID))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- GET (プレビュー) ---------------------------------------------------

// 廃止部署を選択すると移管対象の件数と後継 (現役) select を表示する。
func TestDepartmentMigrate_Form_Preview(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	from := seedMigrateDept(t, q, "MIG-PRV-F", "旧開発部", true)
	to := seedMigrateDept(t, q, "MIG-PRV-T", "開発部", false)
	v := seedVendor(t, q, "PreviewVendor")
	p1 := seedProduct(t, q, v.ID, "PreviewTool1")
	p2 := seedProduct(t, q, v.ID, "PreviewTool2")
	seedMigrateLicense(t, q, p1.ID, from.ID, "keiyaku-1")
	seedMigrateLicense(t, q, p2.ID, from.ID, "keiyaku-2")
	// 衝突見込み 1 件: 後継に同 (product, slug)。
	seedMigrateLicense(t, q, p1.ID, to.ID, "keiyaku-1")
	seedActiveApproval(t, q, from.ID, p1.ID, "approved", "department_wide", nil)

	// 未選択: 廃止部署の select に旧開発部が出る。
	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "旧開発部")

	// ?from= 選択: ライセンス数 / アクティブ承認数 / 後継候補 (現役)。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate?from="+itoa64(from.ID), middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "所管ライセンス")
	handlertest.AssertContains(t, rec, "アクティブ承認")
	handlertest.AssertContains(t, rec, "開発部")

	// ?from=&to= 選択: 衝突見込みを表示する。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate?from="+itoa64(from.ID)+"&to="+itoa64(to.ID),
		middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "衝突見込み")

	// 現役部署は移管元に選択できない (直接 URL でも 404)。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/departments/migrate?from="+itoa64(to.ID), middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// --- POST (移管成功) -----------------------------------------------------

// 移管はライセンス所管変更 + 承認コピー (note 付き) + audit を 1 回で行う。
func TestDepartmentMigrate_Success(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	from := seedMigrateDept(t, q, "MIG-OK-F", "旧設計部", true)
	to := seedMigrateDept(t, q, "MIG-OK-T", "設計部", false)
	v := seedVendor(t, q, "SuccessVendor")
	p1 := seedProduct(t, q, v.ID, "SuccessTool1")
	p2 := seedProduct(t, q, v.ID, "SuccessTool2")
	lic1 := seedMigrateLicense(t, q, p1.ID, from.ID, "keiyaku-a")
	lic2 := seedMigrateLicense(t, q, p2.ID, from.ID, "keiyaku-b")
	original := seedActiveApproval(t, q, from.ID, p1.ID, "approved", "department_wide", nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/departments/migrate",
		middleware.RoleSystemAdmin, migrateForm(from.ID, to.ID))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス移管 2 件")
	handlertest.AssertContains(t, rec, "承認コピー 1 件")

	// ライセンスの所管が後継に変わる。
	if got := licenseOwner(t, q, lic1.ID); got != to.ID {
		t.Errorf("license1 owner = %d, want %d (後継)", got, to.ID)
	}
	if got := licenseOwner(t, q, lic2.ID); got != to.ID {
		t.Errorf("license2 owner = %d, want %d (後継)", got, to.ID)
	}

	// 承認は後継に新規コピーされ、note に移管元を記録する。
	copied, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: to.ID,
		ProductID:    p1.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval(後継): %v", err)
	}
	if copied.Status != "approved" {
		t.Errorf("copied status = %s, want approved", copied.Status)
	}
	if copied.Note == nil || *copied.Note != "部署改廃により 旧設計部 から移管" {
		t.Errorf("copied note = %v, want 部署改廃により 旧設計部 から移管", copied.Note)
	}
	if copied.ApprovedByAppUserID == nil {
		t.Error("copied approved_by_app_user_id is nil, want 実行者の app_user id")
	}

	// 元の承認は取り消さず残す (廃止部署の履歴、Plan の決定)。
	still, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: from.ID,
		ProductID:    p1.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval(廃止部署): %v", err)
	}
	if still.ID != original.ID {
		t.Errorf("original approval id = %d, want %d (残置)", still.ID, original.ID)
	}

	// audit department.migrate 1 行 (entity = 廃止部署)。
	diffs := migrateAuditDiffs(t, db, from.ID)
	if len(diffs) != 1 {
		t.Fatalf("audit department.migrate count = %d, want 1", len(diffs))
	}
	assertMigrateDiff(t, diffs[0], from.ID, to.ID, 2, 0, 1, 0)
}

// --- POST (スキップ) -----------------------------------------------------

// slug 衝突ライセンスは残置、後継に既存アクティブ承認がある product は
// コピーせず、それぞれスキップ件数として報告する。
func TestDepartmentMigrate_SkipsConflictsAndDuplicates(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	from := seedMigrateDept(t, q, "MIG-SKIP-F", "旧営業部", true)
	to := seedMigrateDept(t, q, "MIG-SKIP-T", "営業部", false)
	v := seedVendor(t, q, "SkipVendor")
	p1 := seedProduct(t, q, v.ID, "SkipTool1")
	p2 := seedProduct(t, q, v.ID, "SkipTool2")
	conflicted := seedMigrateLicense(t, q, p1.ID, from.ID, "keiyaku-x")
	movable := seedMigrateLicense(t, q, p2.ID, from.ID, "keiyaku-y")
	existing := seedMigrateLicense(t, q, p1.ID, to.ID, "keiyaku-x")
	seedActiveApproval(t, q, from.ID, p1.ID, "approved", "department_wide", nil)
	seedActiveApproval(t, q, from.ID, p2.ID, "approved", "department_wide", nil)
	dup := seedActiveApproval(t, q, to.ID, p1.ID, "prohibited", "department_wide", nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/departments/migrate",
		middleware.RoleSystemAdmin, migrateForm(from.ID, to.ID))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス移管 1 件")
	handlertest.AssertContains(t, rec, "承認コピー 1 件")
	// スキップありのときは手動対応 → 再実行を促す。
	handlertest.AssertContains(t, rec, "再実行")

	// 衝突ライセンスは廃止部署に残置、他は移管される。
	if got := licenseOwner(t, q, conflicted.ID); got != from.ID {
		t.Errorf("conflicted license owner = %d, want %d (残置)", got, from.ID)
	}
	if got := licenseOwner(t, q, movable.ID); got != to.ID {
		t.Errorf("movable license owner = %d, want %d (後継)", got, to.ID)
	}
	if got := licenseOwner(t, q, existing.ID); got != to.ID {
		t.Errorf("existing license owner = %d, want %d (不変)", got, to.ID)
	}

	// 後継の p1 承認は既存の行のまま (上書き・コピーしない)。
	a, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: to.ID,
		ProductID:    p1.ID,
	})
	if err != nil {
		t.Fatalf("GetActiveApproval(後継 p1): %v", err)
	}
	if a.ID != dup.ID {
		t.Errorf("後継 p1 approval id = %d, want %d (既存を維持)", a.ID, dup.ID)
	}
	// p2 はコピーされる。
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: to.ID,
		ProductID:    p2.ID,
	}); err != nil {
		t.Fatalf("GetActiveApproval(後継 p2): %v", err)
	}

	diffs := migrateAuditDiffs(t, db, from.ID)
	if len(diffs) != 1 {
		t.Fatalf("audit department.migrate count = %d, want 1", len(diffs))
	}
	assertMigrateDiff(t, diffs[0], from.ID, to.ID, 1, 1, 1, 1)
}

// --- POST (冪等性) -------------------------------------------------------

// 再実行しても安全: 移管済み分は対象 0 件、コピー済み承認はスキップ側に
// 回るだけで二重コピーしない。
func TestDepartmentMigrate_RerunIsIdempotent(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	from := seedMigrateDept(t, q, "MIG-RE-F", "旧企画部", true)
	to := seedMigrateDept(t, q, "MIG-RE-T", "企画部", false)
	v := seedVendor(t, q, "RerunVendor")
	p := seedProduct(t, q, v.ID, "RerunTool")
	lic := seedMigrateLicense(t, q, p.ID, from.ID, "keiyaku-r")
	seedActiveApproval(t, q, from.ID, p.ID, "approved", "department_wide", nil)

	for i := 0; i < 2; i++ {
		req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/departments/migrate",
			middleware.RoleSystemAdmin, migrateForm(from.ID, to.ID))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		handlertest.AssertStatus(t, rec, http.StatusOK)
	}

	if got := licenseOwner(t, q, lic.ID); got != to.ID {
		t.Errorf("license owner = %d, want %d", got, to.ID)
	}
	// 後継のアクティブ承認は 1 件のまま (二重コピーなし)。
	var n int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM department_product_approvals
		 WHERE department_id = ? AND product_id = ? AND revoked_at IS NULL`,
		to.ID, p.ID).Scan(&n); err != nil {
		t.Fatalf("count active approvals: %v", err)
	}
	if n != 1 {
		t.Errorf("後継のアクティブ承認 = %d 件, want 1 (二重コピーなし)", n)
	}

	diffs := migrateAuditDiffs(t, db, from.ID)
	if len(diffs) != 2 {
		t.Fatalf("audit department.migrate count = %d, want 2 (実行ごとに 1 行)", len(diffs))
	}
	assertMigrateDiff(t, diffs[0], from.ID, to.ID, 1, 0, 1, 0)
	// 2 回目: 移管対象 0 件、元の承認は残置のためスキップ 1 件で報告される。
	assertMigrateDiff(t, diffs[1], from.ID, to.ID, 0, 0, 0, 1)
}

// --- POST (検証) ---------------------------------------------------------

// from は廃止部署のみ、to は現役部署のみ、同一部署は不可 (いずれも 400)。
func TestDepartmentMigrate_Validation_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	retired := seedMigrateDept(t, q, "MIG-VAL-R", "旧監査部", true)
	retired2 := seedMigrateDept(t, q, "MIG-VAL-R2", "旧経理部", true)
	active := seedMigrateDept(t, q, "MIG-VAL-A", "監査部", false)
	active2 := seedMigrateDept(t, q, "MIG-VAL-A2", "経理部", false)
	v := seedVendor(t, q, "ValVendor")
	p := seedProduct(t, q, v.ID, "ValTool")
	lic := seedMigrateLicense(t, q, p.ID, retired.ID, "keiyaku-v")

	cases := []struct {
		name string
		form url.Values
	}{
		{"from が現役", migrateForm(active.ID, active2.ID)},
		{"to が廃止", migrateForm(retired.ID, retired2.ID)},
		{"from == to", migrateForm(retired.ID, retired.ID)},
		{"from が不存在", migrateForm(999999, active.ID)},
		{"to が不存在", migrateForm(retired.ID, 999999)},
		{"from が非数値", url.Values{"from": {"abc"}, "to": {itoa64(active.ID)}}},
	}
	for _, tc := range cases {
		req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/departments/migrate",
			middleware.RoleSystemAdmin, tc.form)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, rec.Code)
		}
	}

	// 検証エラーでは何も動かない: 所管不変・承認なし・audit なし。
	if got := licenseOwner(t, q, lic.ID); got != retired.ID {
		t.Errorf("license owner = %d, want %d (不変)", got, retired.ID)
	}
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: active.ID,
		ProductID:    p.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetActiveApproval(現役) = %v, want sql.ErrNoRows", err)
	}
	if diffs := migrateAuditDiffs(t, db, retired.ID); len(diffs) != 0 {
		t.Errorf("audit department.migrate count = %d, want 0", len(diffs))
	}
}
