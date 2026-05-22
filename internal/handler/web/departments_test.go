package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// /departments は要件書 §11 で「viewer 以上」と規定されており、
// vendors / products と異なり general_user は閲覧できない。
// この差を明示するテストを最初に置く。

func TestDepartments_List_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDepartments_List_Viewer_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "部署")
}

func TestDepartments_List_ShowsExistingDepartments(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	}); err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}
	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT002",
		Name: "製造本部",
	}); err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/departments", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "DEPT001")
	handlertest.AssertContains(t, rec, "営業本部")
	handlertest.AssertContains(t, rec, "DEPT002")
	handlertest.AssertContains(t, rec, "製造本部")
}

func TestDepartments_List_HidesNewButton_ForViewer(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, `href="/departments/new"`) {
		t.Errorf("viewer should not see /departments/new link, body=\n%s", body)
	}
}

func TestDepartments_List_ShowsNewButton_ForLicenseManager(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `href="/departments/new"`)
}

func TestDepartments_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="code"`)
	handlertest.AssertContains(t, rec, `name="name"`)
}

func TestDepartments_NewForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/departments/new", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}
