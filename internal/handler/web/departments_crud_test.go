package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
