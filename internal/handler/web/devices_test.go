package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestDevices_List_ExcludesInactive_ByDefault(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-X",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteDevice(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	if body := rec.Body.String(); contains(body, "PC-X") {
		t.Errorf("default list should not show retired device, body=\n%s", body)
	}
}

func TestDevices_List_IncludesInactive_WithQueryParam(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-X",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteDevice(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices?include_inactive=1", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	handlertest.AssertContains(t, rec, "PC-X")
	handlertest.AssertContains(t, rec, "退役")
	handlertest.AssertContains(t, rec, "row-inactive")
}

func TestDevices_List_IncludeInactiveCheckbox_Rendered(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="include_inactive"`)
}

func TestDevices_Search_ExcludesInactive_ByDefault(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-X",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteDevice(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices?q=PC-", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
	if body := rec.Body.String(); contains(body, "PC-X") {
		t.Errorf("default search should not show retired device, body=\n%s", body)
	}
}

func TestDevices_Show_RetiredBadgeShown(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.SoftDeleteDevice(context.Background(), d.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices/"+strconv.FormatInt(d.ID, 10), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "退役")
}

func TestDevices_Show_LastSeenAtRendersUnknownWhenNull(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/devices/"+strconv.FormatInt(d.ID, 10), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "最終確認")
	handlertest.AssertContains(t, rec, "(未確認)")
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
