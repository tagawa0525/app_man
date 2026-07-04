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
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// licenseSeed は licenses テストの前提レコード (vendor / product / 部署)。
type licenseSeed struct {
	vendor  repository.Vendor
	product repository.Product
	dept    repository.Department
}

// seedLicenseCatalog は vendor + product + 現役部署を 1 組投入する。
func seedLicenseCatalog(t *testing.T, q *repository.Queries, vendorName, productName, deptCode, deptName string) licenseSeed {
	t.Helper()
	v := seedVendor(t, q, vendorName)
	p := seedProduct(t, q, v.ID, productName)
	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: deptCode,
		Name: deptName,
	})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	return licenseSeed{vendor: v, product: p, dept: d}
}

// seedLicense はライセンスを直接 DB に投入する (画面経由でない前提データ)。
func seedLicense(t *testing.T, q *repository.Queries, s licenseSeed, slug, name string, expiresAt *time.Time, keys *string) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          s.product.ID,
		OwningDepartmentID: s.dept.ID,
		LicenseSlug:        slug,
		DisplayName:        name,
		CountUnit:          "device",
		ContractType:       "subscription",
		ExpiresAt:          expiresAt,
		ProductKeys:        keys,
		FsDirPath:          "licenses/seed/seed/" + slug,
	})
	if err != nil {
		t.Fatalf("CreateLicense: %v", err)
	}
	return lic
}

func timePtr(t time.Time) *time.Time { return &t }

func strPtr(s string) *string { return &s }

// validLicenseForm は create / update に使える最小の妥当なフォーム値。
func validLicenseForm(s licenseSeed) url.Values {
	return url.Values{
		"product_id":           {strconv.FormatInt(s.product.ID, 10)},
		"owning_department_id": {strconv.FormatInt(s.dept.ID, 10)},
		"license_slug":         {"契約 2024/上期"},
		"display_name":         {"Acrobat 年間契約"},
		"total_count":          {"10"},
		"count_unit":           {"device"},
		"contract_type":        {"subscription"},
		"currency":             {"JPY"},
	}
}

func TestLicenses_List_Viewer_200(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス")
}

func TestLicenses_List_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	// 要件書 §6.1: /licenses は viewer 以上。general_user は 403。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestLicenses_List_DefaultHidesExpired(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	seedLicense(t, q, s, "active", "現役ライセンス", timePtr(time.Now().AddDate(0, 0, 200)), nil)
	seedLicense(t, q, s, "expired", "満了ライセンス", timePtr(time.Now().AddDate(0, 0, -30)), nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "現役ライセンス")
	if strings.Contains(rec.Body.String(), "満了ライセンス") {
		t.Errorf("default list should hide expired license, body:\n%s", rec.Body.String())
	}
	// 満了込み表示へのトグルリンクがある。
	handlertest.AssertContains(t, rec, "?expired=1")
}

func TestLicenses_List_ExpiredParamShowsExpired(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	seedLicense(t, q, s, "active", "現役ライセンス", timePtr(time.Now().AddDate(0, 0, 200)), nil)
	seedLicense(t, q, s, "expired", "満了ライセンス", timePtr(time.Now().AddDate(0, 0, -30)), nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses?expired=1", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "現役ライセンス")
	handlertest.AssertContains(t, rec, "満了ライセンス")
}

func TestLicenses_List_MarksExpiringSoon(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	seedLicense(t, q, s, "soon", "まもなく満了", timePtr(time.Now().AddDate(0, 0, 30)), nil)
	seedLicense(t, q, s, "later", "余裕あり", timePtr(time.Now().AddDate(0, 0, 200)), nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	// 期限 90 日以内の行だけに警告が付く。
	if got := strings.Count(rec.Body.String(), "期限接近"); got != 1 {
		t.Errorf("期限接近 count = %d, want 1, body:\n%s", got, rec.Body.String())
	}
}

func TestLicenses_List_ExpiryBoundaries(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// 判定は UTC 日付単位: 昨日 = 満了、ちょうど 90 日後 = 期限接近、
	// 91 日後 = 非接近 (SQL の date('now') 判定と同じ基準)。
	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	seedLicense(t, q, s, "yesterday", "昨日満了分", timePtr(time.Now().AddDate(0, 0, -1)), nil)
	seedLicense(t, q, s, "day90", "90日後満了分", timePtr(time.Now().AddDate(0, 0, 90)), nil)
	seedLicense(t, q, s, "day91", "91日後満了分", timePtr(time.Now().AddDate(0, 0, 91)), nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses?expired=1", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	// 満了バッジは昨日満了の 1 行だけ ("満了 (" は badge のみに現れる)。
	if got := strings.Count(body, "満了 ("); got != 1 {
		t.Errorf("満了バッジ count = %d, want 1, body:\n%s", got, body)
	}
	// 期限接近はちょうど 90 日後の 1 行だけ (91 日後は非接近)。
	if got := strings.Count(body, "期限接近"); got != 1 {
		t.Errorf("期限接近 count = %d, want 1, body:\n%s", got, body)
	}
}

func TestLicenses_Show_HidesProductKeys(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Acrobat 年間契約")
	// product_keys は平文表示せず「登録あり」のみ。
	handlertest.AssertContains(t, rec, "登録あり")
	if strings.Contains(rec.Body.String(), "SECRET-KEY-XYZ-999") {
		t.Errorf("show must not render raw product_keys, body:\n%s", rec.Body.String())
	}
}

func TestLicenses_Show_NoKeys_ShowsUnregistered(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "登録なし")
	// fs_dir_path も表示される。
	handlertest.AssertContains(t, rec, "licenses/seed/seed/base")
}

func TestLicenses_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses/9999", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestLicenses_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ライセンス新規作成")
	handlertest.AssertContains(t, rec, "Acrobat Pro")
	handlertest.AssertContains(t, rec, "情報システム部")
}

func TestLicenses_NewForm_Viewer_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses/new", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestLicenses_NewForm_ExcludesInactiveDepartments(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	dead, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT-DEAD",
		Name: "廃止済み部署",
	})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	if _, err := q.SoftDeleteDepartment(context.Background(), dead.ID); err != nil {
		t.Fatalf("SoftDeleteDepartment: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/licenses/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "情報システム部")
	if strings.Contains(rec.Body.String(), "廃止済み部署") {
		t.Errorf("new form must not offer inactive departments, body:\n%s", rec.Body.String())
	}
}

func TestLicenses_Create_RedirectsAndComputesFsDirPath(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	// vendor / product 名のスペース、slug の日本語 + 禁止文字 (/ とスペース)
	// が仕様 §3.2 の規則で _ に正規化されることを確認する。
	s := seedLicenseCatalog(t, q, "Adobe Inc", "Acrobat Pro", "DEPT001", "情報システム部")

	form := validLicenseForm(s)
	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	items, err := q.ListLicenses(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListLicenses: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("licenses = %d, want 1", len(items))
	}
	lic := items[0]
	if lic.DisplayName != "Acrobat 年間契約" {
		t.Errorf("display_name = %q", lic.DisplayName)
	}
	wantPath := "licenses/Adobe_Inc/Acrobat_Pro/契約_2024_上期"
	if lic.FsDirPath != wantPath {
		t.Errorf("fs_dir_path = %q, want %q", lic.FsDirPath, wantPath)
	}
	if lic.TotalCount == nil || *lic.TotalCount != 10 {
		t.Errorf("total_count = %v, want 10", lic.TotalCount)
	}
	wantLoc := fmt.Sprintf("/licenses/%d", lic.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
}

func TestLicenses_Create_Viewer_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleViewer, validLicenseForm(s))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestLicenses_Create_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	form := validLicenseForm(s)
	form.Set("display_name", "")
	form.Set("license_slug", "")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	items, err := q.ListLicenses(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListLicenses: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("license should not be created, got %d", len(items))
	}
}

func TestLicenses_Create_RejectsInvalidValues(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")

	cases := []struct {
		name  string
		field string
		value string
	}{
		{"negative total_count", "total_count", "-5"},
		{"non-numeric total_count", "total_count", "abc"},
		{"invalid date format", "expires_at", "2026/01/01"},
		{"unknown count_unit", "count_unit", "bogus"},
		{"unknown contract_type", "contract_type", "rental"},
		{"negative unit_price", "unit_price", "-100"},
	}
	for _, tc := range cases {
		form := validLicenseForm(s)
		form.Set(tc.field, tc.value)
		req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	items, err := q.ListLicenses(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListLicenses: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("no license should be created, got %d", len(items))
	}
}

func TestLicenses_Create_DuplicateSlug_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	seedLicense(t, q, s, "契約 2024/上期", "既存契約", nil, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, validLicenseForm(s))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "重複")
}

func TestLicenses_Create_RejectsInactiveDepartment(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	if _, err := q.SoftDeleteDepartment(context.Background(), s.dept.ID); err != nil {
		t.Fatalf("SoftDeleteDepartment: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, validLicenseForm(s))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestLicenses_EditForm_DoesNotPrefillProductKeys(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d/edit", lic.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "SECRET-KEY-XYZ-999") {
		t.Errorf("edit form must not prefill product_keys, body:\n%s", rec.Body.String())
	}
	// write-only 運用の説明文。
	handlertest.AssertContains(t, rec, "空欄のままなら変更されません")
}

func TestLicenses_EditForm_Viewer_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/licenses/%d/edit", lic.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestLicenses_Update_EmptyKeysKeepExisting(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	form := validLicenseForm(s)
	form.Set("license_slug", "base")
	form.Set("display_name", "更新後の名前")
	form.Set("product_keys", "")

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetLicenseByID(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	if got.DisplayName != "更新後の名前" {
		t.Errorf("display_name = %q, want 更新後の名前", got.DisplayName)
	}
	if got.ProductKeys == nil || *got.ProductKeys != "SECRET-KEY-XYZ-999" {
		t.Errorf("product_keys = %v, want existing key kept", got.ProductKeys)
	}
}

func TestLicenses_Update_OverwritesKeysWhenProvided(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("OLD-KEY"))

	form := validLicenseForm(s)
	form.Set("license_slug", "base")
	form.Set("product_keys", "NEW-KEY-123")

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetLicenseByID(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	if got.ProductKeys == nil || *got.ProductKeys != "NEW-KEY-123" {
		t.Errorf("product_keys = %v, want NEW-KEY-123", got.ProductKeys)
	}
}

func TestLicenses_Update_RecomputesFsDirPath(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe Inc", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	form := validLicenseForm(s)
	form.Set("license_slug", "更新後 slug")

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetLicenseByID(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	want := "licenses/Adobe_Inc/Acrobat_Pro/更新後_slug"
	if got.FsDirPath != want {
		t.Errorf("fs_dir_path = %q, want %q", got.FsDirPath, want)
	}
}

func TestLicenses_Update_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses/9999", middleware.RoleLicenseManager, validLicenseForm(s))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}
