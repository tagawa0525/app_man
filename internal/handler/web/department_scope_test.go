package web_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/session"
)

// department_scope_test.go は部署スコープ認可 (仕様 §7.1〜7.2 / Plan
// department-scope-authz.md) の web 層テスト。ルート単位の RequireRole
// (ロール階層) を通過した部署別ロールが、他部署のデータを書込みできない
// ことを各経路で確認する:
//
//   - licenses: create (owning dept) / update (現部署と変更先の両方) /
//     割当追加・解除 / 証書アップロード / キー閲覧 — license_manager 相当
//   - approvals: 登録・取消 — dept_security_admin 相当
//
// 閲覧系 (一覧・詳細・ダウンロード) は §5.6 の全社開示方針のまま変更しない。
// 違反は 403 (RequireRole と同じ体裁)。自部署と system_admin は従来どおり
// 成功する。

// multipartPostWithCookie は cookie 付き multipart/form-data POST を組み立てる
// (documents_test.go の authenticatedMultipartPost の部署スコープ role 対応版。
// cookie を外から渡す形にして AuthenticatedAsInDept と組み合わせる)。
func multipartPostWithCookie(t *testing.T, store session.Store,
	target string, cookie *http.Cookie, fields map[string]string,
	fileName string, fileContent []byte,
) *http.Request {
	t.Helper()
	sess, err := store.GetByID(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("multipartPostWithCookie: GetByID(%q): %v", cookie.Value, err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("_csrf", sess.CSRFToken); err != nil {
		t.Fatalf("write _csrf field: %v", err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileName != "" {
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(fileContent); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	return req
}

// twoDeptCatalogs は部署 A / B それぞれの vendor + product + 部署を投入する。
func twoDeptCatalogs(t *testing.T, q *repository.Queries) (a, b licenseSeed) {
	t.Helper()
	a = seedLicenseCatalog(t, q, "VendorA", "ProdA", "SCOPEA", "部署A")
	b = seedLicenseCatalog(t, q, "VendorB", "ProdB", "SCOPEB", "部署B")
	return a, b
}

// --- licenses: create -----------------------------------------------------

func TestDeptScope_Licenses_Create(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	a, b := twoDeptCatalogs(t, q)

	// 部署 A の license_manager が部署 B を所管にした新規作成 → 403。
	req := handlertest.AuthenticatedPostFormInDept(t, db, store, "/licenses",
		middleware.RoleLicenseManager, a.dept.ID, validLicenseForm(b))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	// editors 束に含まれる dept_security_admin にも同じ部署制限 (§7.1 の
	// 「自部署」列はロールが上位でも変わらない)。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, "/licenses",
		middleware.RoleDepartmentSecurityAdmin, a.dept.ID, validLicenseForm(b))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	items, err := q.ListLicenses(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListLicenses: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("licenses = %d, want 0 (他部署の作成は拒否)", len(items))
	}

	// 自部署 B の license_manager は従来どおり成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, "/licenses",
		middleware.RoleLicenseManager, b.dept.ID, validLicenseForm(b))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("own dept create: status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	// system_admin は部署スコープ行 (部署 A) でも全部署で成功。
	form := validLicenseForm(b)
	form.Set("license_slug", "sa-created")
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, "/licenses",
		middleware.RoleSystemAdmin, a.dept.ID, form)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("system_admin create: status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
}

// --- licenses: update -----------------------------------------------------

func TestDeptScope_Licenses_Update(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	a, b := twoDeptCatalogs(t, q)
	licB := seedLicense(t, q, b, "base-b", "部署Bの契約", nil, nil)

	form := validLicenseForm(b)
	form.Set("license_slug", "base-b")
	form.Set("display_name", "他部署から改ざん")

	// 部署 A の license_manager が部署 B 所管ライセンスを更新 → 403。
	req := handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d", licB.ID), middleware.RoleLicenseManager, a.dept.ID, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	got, err := q.GetLicenseByID(context.Background(), licB.ID)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	if got.DisplayName != "部署Bの契約" {
		t.Fatalf("display_name = %q, want 部署Bの契約 (未変更)", got.DisplayName)
	}

	// 自部署 B の license_manager でも、変更先を部署 A にする更新は 403
	// (現部署と変更先の両方をチェック)。
	moveForm := validLicenseForm(b)
	moveForm.Set("license_slug", "base-b")
	moveForm.Set("owning_department_id", strconv.FormatInt(a.dept.ID, 10))
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d", licB.ID), middleware.RoleLicenseManager, b.dept.ID, moveForm)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	got, err = q.GetLicenseByID(context.Background(), licB.ID)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	if got.OwningDepartmentID != b.dept.ID {
		t.Fatalf("owning_department_id = %d, want %d (未変更)", got.OwningDepartmentID, b.dept.ID)
	}

	// 自部署 B のままの更新は従来どおり成功。
	okForm := validLicenseForm(b)
	okForm.Set("license_slug", "base-b")
	okForm.Set("display_name", "自部署で更新")
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d", licB.ID), middleware.RoleLicenseManager, b.dept.ID, okForm)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("own dept update: status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	// system_admin (部署 A スコープ行) は部署 B → A の付け替えも成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d", licB.ID), middleware.RoleSystemAdmin, a.dept.ID, moveForm)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("system_admin update: status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	// 閲覧系は変更しない: 部署 A の license_manager でも詳細は 200 (§5.6)。
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/licenses/%d", licB.ID), nil)
	getReq.AddCookie(handlertest.AuthenticatedAsInDept(t, db, store, middleware.RoleLicenseManager, a.dept.ID))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, getReq)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- licenses: 割当追加・解除 ----------------------------------------------

func TestDeptScope_Assignments(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	a, b := twoDeptCatalogs(t, q)
	licB := seedLicense(t, q, b, "asg-b", "部署Bの契約", nil, nil)
	u := seedActiveUser(t, q, "E900", "割当先ユーザ")
	d := seedActiveDevice(t, q, "PC-900")

	// 部署 A の license_manager による割当追加 → 403。
	req := handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/users", licB.ID),
		middleware.RoleLicenseManager, a.dept.ID,
		url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/devices", licB.ID),
		middleware.RoleLicenseManager, a.dept.ID,
		url.Values{"device_id": {strconv.FormatInt(d.ID, 10)}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	if asgs, err := q.ListActiveUserAssignmentsByLicense(context.Background(), licB.ID); err != nil || len(asgs) != 0 {
		t.Fatalf("user assignments = %d (err %v), want 0", len(asgs), err)
	}
	if asgs, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), licB.ID); err != nil || len(asgs) != 0 {
		t.Fatalf("device assignments = %d (err %v), want 0", len(asgs), err)
	}

	// 直接 seed した割当の解除も、部署 A の license_manager は 403。
	uAsg, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: licB.ID, UserID: u.ID,
	})
	if err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}
	dAsg, err := q.CreateDeviceAssignment(context.Background(), repository.CreateDeviceAssignmentParams{
		LicenseID: licB.ID, DeviceID: d.ID,
	})
	if err != nil {
		t.Fatalf("CreateDeviceAssignment: %v", err)
	}

	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/users/%d/revoke", licB.ID, uAsg.ID),
		middleware.RoleLicenseManager, a.dept.ID, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/devices/%d/revoke", licB.ID, dAsg.ID),
		middleware.RoleLicenseManager, a.dept.ID, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	if asgs, err := q.ListActiveUserAssignmentsByLicense(context.Background(), licB.ID); err != nil || len(asgs) != 1 {
		t.Fatalf("user assignments after denied revoke = %d (err %v), want 1", len(asgs), err)
	}

	// 自部署 B の license_manager は解除成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/users/%d/revoke", licB.ID, uAsg.ID),
		middleware.RoleLicenseManager, b.dept.ID, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, fmt.Sprintf("/licenses/%d", licB.ID))

	// system_admin (部署 A スコープ行) も解除成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/assignments/devices/%d/revoke", licB.ID, dAsg.ID),
		middleware.RoleSystemAdmin, a.dept.ID, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, fmt.Sprintf("/licenses/%d", licB.ID))
}

// --- licenses: 証書アップロード ---------------------------------------------

func TestDeptScope_Documents_Upload(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)
	a, b := twoDeptCatalogs(t, q)
	licB := seedLicense(t, q, b, "doc-b", "部署Bの契約", nil, nil)

	// 部署 A の license_manager によるアップロード → 403。
	cookieA := handlertest.AuthenticatedAsInDept(t, db, store, middleware.RoleLicenseManager, a.dept.ID)
	req := multipartPostWithCookie(t, store,
		fmt.Sprintf("/licenses/%d/documents", licB.ID), cookieA,
		map[string]string{"doc_type": "certificate"}, "証書.pdf", pdfContent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), licB.ID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("documents = %d, want 0 (他部署のアップロードは拒否)", len(docs))
	}

	// 自部署 B の license_manager は従来どおり成功。
	cookieB := handlertest.AuthenticatedAsInDept(t, db, store, middleware.RoleLicenseManager, b.dept.ID)
	req = multipartPostWithCookie(t, store,
		fmt.Sprintf("/licenses/%d/documents", licB.ID), cookieB,
		map[string]string{"doc_type": "certificate"}, "証書.pdf", pdfContent)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("own dept upload: status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
}

// --- licenses: キー閲覧 -----------------------------------------------------

func TestDeptScope_Keys_Reveal(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	a, b := twoDeptCatalogs(t, q)
	licB := seedLicense(t, q, b, "key-b", "部署Bの契約", nil, strPtr("SECRET-DEPT-B-KEY-000"))

	// 部署 A の license_manager によるキー閲覧 → 403。キーも audit も出ない。
	req := handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/keys/reveal", licB.ID),
		middleware.RoleLicenseManager, a.dept.ID, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)
	if strings.Contains(rec.Body.String(), "SECRET-DEPT-B-KEY-000") {
		t.Fatalf("denied reveal must not leak keys, body:\n%s", rec.Body.String())
	}
	if got := countAuditLogs(t, db, "license_keys.view", "license", licB.ID); got != 0 {
		t.Fatalf("audit license_keys.view count = %d, want 0 (拒否時は記録しない)", got)
	}

	// 自部署 B の license_manager は従来どおりキーが見える。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store,
		fmt.Sprintf("/licenses/%d/keys/reveal", licB.ID),
		middleware.RoleLicenseManager, b.dept.ID, url.Values{})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "SECRET-DEPT-B-KEY-000")
}

// --- approvals: 登録・取消 --------------------------------------------------

func TestDeptScope_Approvals_GrantRevoke(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	deptA, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "SCOPEA", Name: "部署A",
	})
	if err != nil {
		t.Fatalf("CreateDepartment A: %v", err)
	}
	deptB, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "SCOPEB", Name: "部署B",
	})
	if err != nil {
		t.Fatalf("CreateDepartment B: %v", err)
	}
	v := seedApprovalVendor(t, q)
	p := seedApprovalProduct(t, q, v.ID, "ScopedApp", "department_discretion")

	// 部署 A の dept_security_admin が部署 B へ承認登録 → 403。
	req := handlertest.AuthenticatedPostFormInDept(t, db, store, approvalDetailPath(deptB.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, deptA.ID, url.Values{"status": {"approved"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: deptB.ID, ProductID: p.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveApproval = %v, want sql.ErrNoRows (登録されない)", err)
	}

	// 閲覧 (詳細 GET) は変更しない: 部署 A の dept_security_admin でも 200。
	getReq := httptest.NewRequest(http.MethodGet, approvalDetailPath(deptB.ID, p.ID), nil)
	getReq.AddCookie(handlertest.AuthenticatedAsInDept(t, db, store, middleware.RoleDepartmentSecurityAdmin, deptA.ID))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, getReq)
	handlertest.AssertStatus(t, rec, http.StatusOK)

	// 自部署 A への登録は従来どおり成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, approvalDetailPath(deptA.ID, p.ID),
		middleware.RoleDepartmentSecurityAdmin, deptA.ID, url.Values{"status": {"approved"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, approvalDetailPath(deptA.ID, p.ID))

	// 部署 B のアクティブ承認の取消も、部署 A の dept_security_admin は 403。
	seedActiveApproval(t, q, deptB.ID, p.ID, "approved", "department_wide", nil)
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, approvalDetailPath(deptB.ID, p.ID)+"/revoke",
		middleware.RoleDepartmentSecurityAdmin, deptA.ID, url.Values{"revoke_reason": {"他部署からの取消"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: deptB.ID, ProductID: p.ID,
	}); err != nil {
		t.Fatalf("GetActiveApproval = %v, want active row to survive", err)
	}

	// system_admin (部署 A スコープ行) は部署 B の取消も成功。
	req = handlertest.AuthenticatedPostFormInDept(t, db, store, approvalDetailPath(deptB.ID, p.ID)+"/revoke",
		middleware.RoleSystemAdmin, deptA.ID, url.Values{"revoke_reason": {"全社管理者による取消"}})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertRedirect(t, rec, approvalDetailPath(deptB.ID, p.ID))
	if _, err := q.GetActiveApproval(context.Background(), repository.GetActiveApprovalParams{
		DepartmentID: deptB.ID, ProductID: p.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveApproval after system_admin revoke = %v, want sql.ErrNoRows", err)
	}
}
