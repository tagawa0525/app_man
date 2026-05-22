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

// /devices は要件書 §6.1 で「viewer 以上」と規定されているため
// general_user は閲覧不可。departmentViewers をそのまま流用するため
// テスト構成は users_test.go / departments_test.go と対称。

func TestDevices_List_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_List_Viewer_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "端末")
}

func TestDevices_List_ShowsExistingDevices(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed CreateDevice: %v", err)
	}
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-002",
	}); err != nil {
		t.Fatalf("seed CreateDevice: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	handlertest.AssertContains(t, rec, "PC-002")
}

func TestDevices_List_HidesNewButton_ForViewer(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, `href="/devices/new"`) {
		t.Errorf("viewer should not see /devices/new link, body=\n%s", body)
	}
}

func TestDevices_List_ShowsNewButton_ForLicenseManager(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `href="/devices/new"`)
}

func TestDevices_NewForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices/new", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="asset_code"`)
	handlertest.AssertContains(t, rec, `name="hostname"`)
	handlertest.AssertContains(t, rec, `name="primary_user_id"`)
	handlertest.AssertContains(t, rec, `name="department_id"`)
}

func TestDevices_Search_MatchesAssetCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "SV-999",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices?q=PC-001", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	if body := rec.Body.String(); contains(body, "SV-999") {
		t.Errorf("search PC-001 should not match SV-999, body=\n%s", body)
	}
}

func TestDevices_Search_MatchesHostname(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	hostname1 := "tagawa-pc"
	hostname2 := "yamada-pc"
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
		Hostname:  &hostname1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-002",
		Hostname:  &hostname2,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices?q=tagawa", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	if body := rec.Body.String(); contains(body, "PC-002") {
		t.Errorf("search tagawa should not match yamada-pc, body=\n%s", body)
	}
}
