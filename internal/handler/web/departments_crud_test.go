package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

func TestDepartments_Create_RedirectsToShow(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/departments", middleware.RoleLicenseManager, url.Values{
		"code": {"DEPT001"},
		"name": {"営業本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	ds, err := q.ListDepartments(context.Background())
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(ds) != 1 || ds[0].Code != "DEPT001" || ds[0].Name != "営業本部" {
		t.Fatalf("after create, departments = %#v", ds)
	}
	if ds[0].Source != "manual" {
		t.Errorf("source = %q, want manual", ds[0].Source)
	}
	wantLoc := fmt.Sprintf("/departments/%d", ds[0].ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
}

func TestDepartments_Create_RejectsEmptyCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/departments", middleware.RoleLicenseManager, url.Values{
		"code": {""},
		"name": {"営業本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "部署コード")

	ds, err := q.ListDepartments(context.Background())
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(ds) != 0 {
		t.Errorf("departments should not be created, got %d", len(ds))
	}
}

func TestDepartments_Create_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/departments", middleware.RoleLicenseManager, url.Values{
		"code": {"DEPT001"},
		"name": {""},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	handlertest.AssertContains(t, rec, "名称")
}

func TestDepartments_Create_RejectsInvalidCodeFormat(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	// 部署コードは ASCII 英数 + ハイフン/アンダースコアのみ許可。
	req := handlertest.PostForm(t, "/departments", middleware.RoleLicenseManager, url.Values{
		"code": {"営業部"},
		"name": {"営業本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "部署コード")
}

func TestDepartments_Create_RejectsDuplicateCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	}); err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}

	req := handlertest.PostForm(t, "/departments", middleware.RoleLicenseManager, url.Values{
		"code": {"DEPT001"},
		"name": {"製造本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "部署コードが重複")
}

func TestDepartments_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/departments", middleware.RoleGeneralUser, url.Values{
		"code": {"DEPT001"},
		"name": {"営業本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDepartments_Show_RendersDetail(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/departments/%d", d.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "DEPT001")
	handlertest.AssertContains(t, rec, "営業本部")
}

func TestDepartments_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments/9999", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestDepartments_Show_ListsChildDepartments(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	parent, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code:     "DEPT001-A",
		Name:     "営業1課",
		ParentID: &parent.ID,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/departments/%d", parent.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "DEPT001-A")
	handlertest.AssertContains(t, rec, "営業1課")
}

func TestDepartments_EditForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/departments/%d/edit", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `value="DEPT001"`)
	handlertest.AssertContains(t, rec, `value="営業本部"`)
}

func TestDepartments_EditForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/departments/%d/edit", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDepartments_Update_RewritesFields(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"code": {"DEPT001"},
		"name": {"営業統括本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	updated, err := q.GetDepartment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDepartment: %v", err)
	}
	if updated.Name != "営業統括本部" {
		t.Errorf("name = %q, want %q", updated.Name, "営業統括本部")
	}
}

func TestDepartments_Delete_SetsValidTo(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/delete", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	wantLoc := fmt.Sprintf("/departments/%d", d.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	got, err := q.GetDepartment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDepartment after delete: %v", err)
	}
	if got.ValidTo == nil {
		t.Fatalf("ValidTo = nil, want non-nil (should be set to today)")
	}
}

func TestDepartments_Delete_HidesFromDefaultList(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	delReq := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/delete", d.ID), middleware.RoleLicenseManager, nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)

	// ListDepartments は valid_to IS NULL のみ返す前提。
	ds, err := q.ListDepartments(context.Background())
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(ds) != 0 {
		t.Errorf("expected no active departments after soft-delete, got %d", len(ds))
	}
}

func TestDepartments_Delete_NotFound_404(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/departments/9999/delete", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestDepartments_Delete_AlreadyDeleted_409(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1 回目: 成功
	r.ServeHTTP(httptest.NewRecorder(),
		handlertest.PostForm(t, fmt.Sprintf("/departments/%d/delete", d.ID), middleware.RoleLicenseManager, nil))

	// 2 回目: 409
	req2 := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/delete", d.ID), middleware.RoleLicenseManager, nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec2.Code, rec2.Body.String())
	}
	handlertest.AssertContains(t, rec2, "既に廃止")
}

func TestDepartments_Delete_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/delete", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDepartments_Restore_ClearsValidTo(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 一度廃止
	if _, derr := q.SoftDeleteDepartment(context.Background(), d.ID); derr != nil {
		t.Fatalf("soft delete: %v", derr)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/restore", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetDepartment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDepartment after restore: %v", err)
	}
	if got.ValidTo != nil {
		t.Errorf("ValidTo = %v, want nil", got.ValidTo)
	}
}

func TestDepartments_Restore_AlreadyActive_409(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/restore", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "既に現役")
}

func TestDepartments_Restore_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d/restore", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDepartments_Update_RejectsSelfAsParent(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"code":      {d.Code},
		"name":      {d.Name},
		"parent_id": {strconv.FormatInt(d.ID, 10)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "自身を親")
}

func TestDepartments_Update_RejectsSelfAsSuccessor(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"code":                    {d.Code},
		"name":                    {d.Name},
		"successor_department_id": {strconv.FormatInt(d.ID, 10)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "自身を後継")
}

func TestDepartments_EditForm_PinsInactiveParent(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	parent, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT-PARENT",
		Name: "親部署",
	})
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	child, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code:     "DEPT-CHILD",
		Name:     "子部署",
		ParentID: &parent.ID,
	})
	if err != nil {
		t.Fatalf("seed child: %v", err)
	}
	// 親を廃止
	if _, derr := q.SoftDeleteDepartment(context.Background(), parent.ID); derr != nil {
		t.Fatalf("soft delete parent: %v", derr)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/departments/%d/edit", child.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	// 廃止親が「(廃止)」付きで select に残る
	handlertest.AssertContains(t, rec, "DEPT-PARENT")
	handlertest.AssertContains(t, rec, "(廃止)")
}

func TestDepartments_Update_RejectsDuplicateCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	}); err != nil {
		t.Fatalf("seed1: %v", err)
	}
	d2, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT002",
		Name: "製造本部",
	})
	if err != nil {
		t.Fatalf("seed2: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/departments/%d", d2.ID), middleware.RoleLicenseManager, url.Values{
		"code": {"DEPT001"},
		"name": {"製造本部"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "部署コードが重複")
}
