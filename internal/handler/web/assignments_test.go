package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// seedAssignLicense は count_unit / total_count を指定してライセンスを
// 直接 DB に投入する (割当テストの前提データ)。
func seedAssignLicense(t *testing.T, q *repository.Queries, s licenseSeed, slug, name, countUnit string, totalCount *int64) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          s.product.ID,
		OwningDepartmentID: s.dept.ID,
		LicenseSlug:        slug,
		DisplayName:        name,
		TotalCount:         totalCount,
		CountUnit:          countUnit,
		ContractType:       "subscription",
		FsDirPath:          "licenses/seed/seed/" + slug,
	})
	if err != nil {
		t.Fatalf("CreateLicense: %v", err)
	}
	return lic
}

// seedActiveUser は在職ユーザを投入する。
func seedActiveUser(t *testing.T, q *repository.Queries, code, name string) repository.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: code,
		Name:         name,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// seedActiveDevice は現役端末を投入する。
func seedActiveDevice(t *testing.T, q *repository.Queries, assetCode string) repository.Device {
	t.Helper()
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: assetCode,
	})
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	return d
}

func int64Ptr(v int64) *int64 { return &v }

func TestAssignments_AssignUser_RedirectsAndPersists(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")

	form := url.Values{
		"user_id":             {strconv.FormatInt(u.ID, 10)},
		"external_account_id": {"kou@example.com"},
		"note":                {"棚卸しメモ"},
	}
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusSeeOther)
	wantLoc := fmt.Sprintf("/licenses/%d", lic.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	rows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active user assignments = %d, want 1", len(rows))
	}
	if rows[0].UserID != u.ID {
		t.Errorf("user_id = %d, want %d", rows[0].UserID, u.ID)
	}
	if rows[0].ExternalAccountID == nil || *rows[0].ExternalAccountID != "kou@example.com" {
		t.Errorf("external_account_id = %v, want kou@example.com", rows[0].ExternalAccountID)
	}
	if rows[0].Note == nil || *rows[0].Note != "棚卸しメモ" {
		t.Errorf("note = %v, want 棚卸しメモ", rows[0].Note)
	}

	// 詳細画面に割当行が表示される。
	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, wantLoc, middleware.RoleViewer, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	handlertest.AssertContains(t, showRec, "割当先ユーザ甲")
}

// 空白のみの note / external_account_id は NULL として保存される
// (空文字判定だけだと "   " がそのまま残る)。
func TestAssignments_Assign_WhitespaceOnlyOptionalFieldsBecomeNull(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "WSVendor", "WSProduct", "DEPTWS", "空白検証部")
	lic := seedAssignLicense(t, q, s, "ws", "空白検証契約", "user", nil)
	u := seedActiveUser(t, q, "E900", "空白検証ユーザ")
	d := seedActiveDevice(t, q, "WS-001")

	uform := url.Values{
		"user_id":             {strconv.FormatInt(u.ID, 10)},
		"external_account_id": {"   "},
		"note":                {"  \t "},
	}
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, uform)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusSeeOther)

	urows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(urows) != 1 {
		t.Fatalf("active user assignments = %d, want 1", len(urows))
	}
	if urows[0].ExternalAccountID != nil {
		t.Errorf("external_account_id = %q, want NULL", *urows[0].ExternalAccountID)
	}
	if urows[0].Note != nil {
		t.Errorf("user note = %q, want NULL", *urows[0].Note)
	}

	dform := url.Values{
		"device_id": {strconv.FormatInt(d.ID, 10)},
		"note":      {"   "},
	}
	dreq := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/devices", lic.ID), middleware.RoleLicenseManager, dform)
	drec := httptest.NewRecorder()
	r.ServeHTTP(drec, dreq)
	handlertest.AssertStatus(t, drec, http.StatusSeeOther)

	drows, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveDeviceAssignmentsByLicense: %v", err)
	}
	if len(drows) != 1 {
		t.Fatalf("active device assignments = %d, want 1", len(drows))
	}
	if drows[0].Note != nil {
		t.Errorf("device note = %q, want NULL", *drows[0].Note)
	}
}

func TestAssignments_AssignUser_MissingUserID_400(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestAssignments_AssignUser_Duplicate_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")

	// url.Values は AuthenticatedPostForm が _csrf を埋めるためリクエスト毎に作る。
	first := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager,
		url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}})
	firstRec := httptest.NewRecorder()
	r.ServeHTTP(firstRec, first)
	handlertest.AssertStatus(t, firstRec, http.StatusSeeOther)

	second := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager,
		url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}})
	secondRec := httptest.NewRecorder()
	r.ServeHTTP(secondRec, second)

	if secondRec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", secondRec.Code, secondRec.Body.String())
	}
	handlertest.AssertContains(t, secondRec, "既に割当済み")

	rows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("active user assignments = %d, want 1", len(rows))
	}
}

func TestAssignments_RevokeUser_RemovesFromListKeepsRow(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")
	asg, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: lic.ID,
		UserID:    u.ID,
	})
	if err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users/%d/revoke", lic.ID, asg.ID), middleware.RoleLicenseManager, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusSeeOther)

	rows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("active user assignments = %d, want 0", len(rows))
	}

	// 物理削除ではなく revoked_at の論理解除 (行は残る)。
	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM user_assignments WHERE license_id = ?", lic.ID).Scan(&total); err != nil {
		t.Fatalf("count user_assignments: %v", err)
	}
	if total != 1 {
		t.Errorf("user_assignments rows = %d, want 1 (soft revoke)", total)
	}

	// 詳細画面の割当一覧からは消える。
	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	if strings.Contains(showRec.Body.String(), "割当先ユーザ甲") {
		t.Errorf("revoked assignment should not be listed, body:\n%s", showRec.Body.String())
	}
}

func TestAssignments_RevokeThenReassign_Succeeds(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")
	asg, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: lic.ID,
		UserID:    u.ID,
	})
	if err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}

	revoke := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users/%d/revoke", lic.ID, asg.ID), middleware.RoleLicenseManager, url.Values{})
	revokeRec := httptest.NewRecorder()
	r.ServeHTTP(revokeRec, revoke)
	handlertest.AssertStatus(t, revokeRec, http.StatusSeeOther)

	// 解除済みなら同一ユーザへの再割当は成功する (部分 UNIQUE の対象外)。
	form := url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}}
	again := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, form)
	againRec := httptest.NewRecorder()
	r.ServeHTTP(againRec, again)
	if againRec.Code != http.StatusSeeOther {
		t.Fatalf("reassign status = %d, want 303 (body: %s)", againRec.Code, againRec.Body.String())
	}

	rows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("active user assignments = %d, want 1", len(rows))
	}
}

func TestAssignments_RevokeUser_DoublePost_404(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")
	asg, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: lic.ID,
		UserID:    u.ID,
	})
	if err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}

	path := fmt.Sprintf("/licenses/%d/assignments/users/%d/revoke", lic.ID, asg.ID)
	first := handlertest.AuthenticatedPostForm(t, db, store, path, middleware.RoleLicenseManager, url.Values{})
	firstRec := httptest.NewRecorder()
	r.ServeHTTP(firstRec, first)
	handlertest.AssertStatus(t, firstRec, http.StatusSeeOther)

	second := handlertest.AuthenticatedPostForm(t, db, store, path, middleware.RoleLicenseManager, url.Values{})
	secondRec := httptest.NewRecorder()
	r.ServeHTTP(secondRec, second)
	handlertest.AssertStatus(t, secondRec, http.StatusNotFound)
}

func TestAssignments_AssignUser_Deactivated_400AndNotInOptions(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	gone := seedActiveUser(t, q, "E900", "退職済ユーザ乙")
	if _, err := q.SoftDeleteUser(context.Background(), gone.ID); err != nil {
		t.Fatalf("SoftDeleteUser: %v", err)
	}

	// 退職者 ID の直接 POST は 400。
	form := url.Values{"user_id": {strconv.FormatInt(gone.ID, 10)}}
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	rows, err := q.ListActiveUserAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("active user assignments = %d, want 0", len(rows))
	}

	// 割当フォームの選択肢にも出ない。
	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	if strings.Contains(showRec.Body.String(), "退職済ユーザ乙") {
		t.Errorf("deactivated user must not be offered as option, body:\n%s", showRec.Body.String())
	}
}

func TestAssignments_Show_MarksDeactivatedAssignee(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "在職中に割当済ユーザ")
	if _, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: lic.ID,
		UserID:    u.ID,
	}); err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), u.ID); err != nil {
		t.Fatalf("SoftDeleteUser: %v", err)
	}

	// 既存割当は退職後も表示され、状態注記が付く (未解除割当の可視化)。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "在職中に割当済ユーザ")
	handlertest.AssertContains(t, rec, "退職")
}

func TestAssignments_OverAllocation_WarnsOnUserUnit(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", int64Ptr(1))
	u1 := seedActiveUser(t, q, "E101", "割当ユーザ一号")
	u2 := seedActiveUser(t, q, "E102", "割当ユーザ二号")

	for _, u := range []repository.User{u1, u2} {
		form := url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}}
		req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), middleware.RoleLicenseManager, form)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		// 超過割当はブロックしない (可視化の思想)。2 件目も 303。
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("assign user %d: status = %d, want 303 (body: %s)", u.ID, rec.Code, rec.Body.String())
		}
	}

	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	handlertest.AssertContains(t, showRec, "保有数を超過")
}

func TestAssignments_NoWarning_WhenTotalCountNull(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u1 := seedActiveUser(t, q, "E101", "割当ユーザ一号")
	u2 := seedActiveUser(t, q, "E102", "割当ユーザ二号")

	for _, u := range []repository.User{u1, u2} {
		if _, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
			LicenseID: lic.ID,
			UserID:    u.ID,
		}); err != nil {
			t.Fatalf("CreateUserAssignment: %v", err)
		}
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	// total_count NULL = 無制限契約は超過警告なし。
	if strings.Contains(rec.Body.String(), "保有数を超過") {
		t.Errorf("unlimited license must not warn, body:\n%s", rec.Body.String())
	}
}

func TestAssignments_Warning_UsesCountUnitSide(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// count_unit=device の契約では、ユーザ割当が保有数を超えても警告しない
	// (警告判定は count_unit に一致する側の割当数のみ)。
	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "device", int64Ptr(1))
	u1 := seedActiveUser(t, q, "E101", "割当ユーザ一号")
	u2 := seedActiveUser(t, q, "E102", "割当ユーザ二号")

	for _, u := range []repository.User{u1, u2} {
		if _, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
			LicenseID: lic.ID,
			UserID:    u.ID,
		}); err != nil {
			t.Fatalf("CreateUserAssignment: %v", err)
		}
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "保有数を超過") {
		t.Errorf("device-unit license must not warn on user assignments, body:\n%s", rec.Body.String())
	}
}

func TestAssignments_AssignDevice_RedirectsAndPersists(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "device", nil)
	d := seedActiveDevice(t, q, "PC-ASSIGN-01")

	form := url.Values{
		"device_id": {strconv.FormatInt(d.ID, 10)},
		"note":      {"検証機"},
	}
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/devices", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusSeeOther)
	wantLoc := fmt.Sprintf("/licenses/%d", lic.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	rows, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveDeviceAssignmentsByLicense: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("active device assignments = %d, want 1", len(rows))
	}
	if rows[0].DeviceID != d.ID {
		t.Errorf("device_id = %d, want %d", rows[0].DeviceID, d.ID)
	}
	if rows[0].Note == nil || *rows[0].Note != "検証機" {
		t.Errorf("note = %v, want 検証機", rows[0].Note)
	}

	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, wantLoc, middleware.RoleViewer, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	handlertest.AssertContains(t, showRec, "PC-ASSIGN-01")
}

func TestAssignments_AssignDevice_Duplicate_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "device", nil)
	d := seedActiveDevice(t, q, "PC-ASSIGN-01")

	// url.Values は AuthenticatedPostForm が _csrf を埋めるためリクエスト毎に作る。
	path := fmt.Sprintf("/licenses/%d/assignments/devices", lic.ID)
	first := handlertest.AuthenticatedPostForm(t, db, store, path, middleware.RoleLicenseManager,
		url.Values{"device_id": {strconv.FormatInt(d.ID, 10)}})
	firstRec := httptest.NewRecorder()
	r.ServeHTTP(firstRec, first)
	handlertest.AssertStatus(t, firstRec, http.StatusSeeOther)

	second := handlertest.AuthenticatedPostForm(t, db, store, path, middleware.RoleLicenseManager,
		url.Values{"device_id": {strconv.FormatInt(d.ID, 10)}})
	secondRec := httptest.NewRecorder()
	r.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", secondRec.Code, secondRec.Body.String())
	}
	handlertest.AssertContains(t, secondRec, "既に割当済み")
}

func TestAssignments_RevokeDevice_RemovesFromList(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "device", nil)
	d := seedActiveDevice(t, q, "PC-ASSIGN-01")
	asg, err := q.CreateDeviceAssignment(context.Background(), repository.CreateDeviceAssignmentParams{
		LicenseID: lic.ID,
		DeviceID:  d.ID,
	})
	if err != nil {
		t.Fatalf("CreateDeviceAssignment: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/devices/%d/revoke", lic.ID, asg.ID), middleware.RoleLicenseManager, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusSeeOther)

	rows, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveDeviceAssignmentsByLicense: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("active device assignments = %d, want 0", len(rows))
	}
}

func TestAssignments_AssignDevice_Retired_400AndNotInOptions(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "device", nil)
	d := seedActiveDevice(t, q, "PC-RETIRED-99")
	if _, err := q.SoftDeleteDevice(context.Background(), d.ID); err != nil {
		t.Fatalf("SoftDeleteDevice: %v", err)
	}

	form := url.Values{"device_id": {strconv.FormatInt(d.ID, 10)}}
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d/assignments/devices", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusBadRequest)

	rows, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListActiveDeviceAssignmentsByLicense: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("active device assignments = %d, want 0", len(rows))
	}

	showReq := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, nil)
	showRec := httptest.NewRecorder()
	r.ServeHTTP(showRec, showReq)
	handlertest.AssertStatus(t, showRec, http.StatusOK)
	if strings.Contains(showRec.Body.String(), "PC-RETIRED-99") {
		t.Errorf("retired device must not be offered as option, body:\n%s", showRec.Body.String())
	}
}

func TestAssignments_Options_NotTruncatedAt200(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)

	// 在職ユーザ・現役端末を 201 件ずつ投入する。コード・名前をゼロ埋め
	// 連番にして、201 件目がどの並び順でも末尾に来るようにする。選択肢
	// クエリに LIMIT があると 201 件目が option に出ず、そのユーザ/端末は
	// 画面から割当不能になる。
	for i := 1; i <= 201; i++ {
		seedActiveUser(t, q, fmt.Sprintf("SEL-%03d", i), fmt.Sprintf("選択肢ユーザ%03d", i))
		seedActiveDevice(t, q, fmt.Sprintf("OPT-%03d", i))
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "選択肢ユーザ201")
	handlertest.AssertContains(t, rec, "OPT-201")
}

func TestAssignments_Viewer_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", nil)
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")
	d := seedActiveDevice(t, q, "PC-ASSIGN-01")

	paths := []struct {
		path string
		form url.Values
	}{
		{fmt.Sprintf("/licenses/%d/assignments/users", lic.ID), url.Values{"user_id": {strconv.FormatInt(u.ID, 10)}}},
		{fmt.Sprintf("/licenses/%d/assignments/users/1/revoke", lic.ID), url.Values{}},
		{fmt.Sprintf("/licenses/%d/assignments/devices", lic.ID), url.Values{"device_id": {strconv.FormatInt(d.ID, 10)}}},
		{fmt.Sprintf("/licenses/%d/assignments/devices/1/revoke", lic.ID), url.Values{}},
	}
	for _, tc := range paths {
		req := handlertest.AuthenticatedPostForm(t, db, store, tc.path, middleware.RoleViewer, tc.form)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", tc.path, rec.Code)
		}
	}
}

func TestProducts_Show_LicenseUsageSummary(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedAssignLicense(t, q, s, "base", "Acrobat 年間契約", "user", int64Ptr(7))
	u := seedActiveUser(t, q, "E100", "割当先ユーザ甲")
	d := seedActiveDevice(t, q, "PC-ASSIGN-01")
	if _, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: lic.ID,
		UserID:    u.ID,
	}); err != nil {
		t.Fatalf("CreateUserAssignment: %v", err)
	}
	if _, err := q.CreateDeviceAssignment(context.Background(), repository.CreateDeviceAssignmentParams{
		LicenseID: lic.ID,
		DeviceID:  d.ID,
	}); err != nil {
		t.Fatalf("CreateDeviceAssignment: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/products/%d", s.product.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス利用状況")
	handlertest.AssertContains(t, rec, "保有数: 7")
	handlertest.AssertContains(t, rec, "ユーザ割当: 1")
	handlertest.AssertContains(t, rec, "端末割当: 1")
	// installations は SKYSEA 未取込のため常に 0。
	handlertest.AssertContains(t, rec, "インストール数: 0")
}

func TestProducts_Show_UsageZeroWithoutLicenses(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// ライセンスが 1 件もない製品でも v_license_usage (LEFT JOIN) の
	// 0 集計行で壊れず 0 表示になる。
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat Pro")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/products/%d", p.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス利用状況")
	handlertest.AssertContains(t, rec, "保有数: 0")
	handlertest.AssertContains(t, rec, "ユーザ割当: 0")
	handlertest.AssertContains(t, rec, "端末割当: 0")
	handlertest.AssertContains(t, rec, "インストール数: 0")
}
