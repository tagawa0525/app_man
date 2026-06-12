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

func TestDevices_Create_RedirectsToShow(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	ds, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(ds) != 1 || ds[0].AssetCode != "PC-001" {
		t.Fatalf("after create, devices = %#v", ds)
	}
	wantLoc := fmt.Sprintf("/devices/%d", ds[0].ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
}

func TestDevices_Create_StoresOptionalFields(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	user, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code":      {"PC-001"},
		"hostname":        {"tagawa-pc"},
		"primary_user_id": {strconv.FormatInt(user.ID, 10)},
		"department_id":   {strconv.FormatInt(dept.ID, 10)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	ds, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(ds) != 1 {
		t.Fatalf("got %d devices, want 1", len(ds))
	}
	d := ds[0]
	if d.Hostname == nil || *d.Hostname != "tagawa-pc" {
		t.Errorf("Hostname = %v, want 'tagawa-pc'", d.Hostname)
	}
	if d.PrimaryUserID == nil || *d.PrimaryUserID != user.ID {
		t.Errorf("PrimaryUserID = %v, want %d", d.PrimaryUserID, user.ID)
	}
	if d.DepartmentID == nil || *d.DepartmentID != dept.ID {
		t.Errorf("DepartmentID = %v, want %d", d.DepartmentID, dept.ID)
	}
}

func TestDevices_Create_RejectsEmptyAssetCode(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code": {""},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "資産コード")

	ds, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(ds) != 0 {
		t.Errorf("devices should not be created, got %d", len(ds))
	}
}

func TestDevices_Create_RejectsInvalidAssetCodeFormat(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC 001"}, // 空白は許可されない
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "資産コード")
}

func TestDevices_Create_RejectsTooLongHostname(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
		"hostname":   {strings.Repeat("a", 256)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "ホスト名")
}

func TestDevices_Create_RejectsDuplicateAssetCode(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed CreateDevice: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "資産コードが重複")
}

func TestDevices_Create_RejectsNonExistentPrimaryUserID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code":      {"PC-001"},
		"primary_user_id": {"9999"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "指定されたユーザは存在しません")
}

func TestDevices_Create_RejectsNonExistentDepartmentID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleLicenseManager, url.Values{
		"asset_code":    {"PC-001"},
		"department_id": {"9999"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "指定された部署は存在しません")
}

func TestDevices_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices", middleware.RoleGeneralUser, url.Values{
		"asset_code": {"PC-001"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_Show_RendersDetail(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "PC-001")
}

func TestDevices_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/devices/9999", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestDevices_Show_LinksToPrimaryUser(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	user, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:     "PC-001",
		PrimaryUserID: &user.ID,
	})
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, fmt.Sprintf(`href="/users/%d"`, user.ID))
	handlertest.AssertContains(t, rec, "田川太郎")
}

func TestDevices_Show_LinksToDepartment(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:    "PC-001",
		DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, fmt.Sprintf(`href="/departments/%d"`, dept.ID))
	handlertest.AssertContains(t, rec, "営業本部")
}

func TestDevices_EditForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d/edit", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `value="PC-001"`)
}

func TestDevices_EditForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d/edit", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_Update_RewritesFields(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
		"hostname":   {"tagawa-pc"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	updated, err := q.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if updated.Hostname == nil || *updated.Hostname != "tagawa-pc" {
		t.Errorf("Hostname = %v, want 'tagawa-pc'", updated.Hostname)
	}
}

func TestDevices_Update_ClearsOptionalFieldsToNull(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	hostname := "tagawa-pc"
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
		Hostname:  &hostname,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
		"hostname":   {""},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	updated, err := q.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if updated.Hostname != nil {
		t.Errorf("Hostname = %v, want nil", *updated.Hostname)
	}
}

func TestDevices_Update_RejectsDuplicateAssetCode(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	}); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-002",
	})
	if err != nil {
		t.Fatalf("seed second: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d", d.ID), middleware.RoleLicenseManager, url.Values{
		"asset_code": {"PC-001"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "資産コードが重複")
}

func TestDevices_Retire_SetsRetiredAt(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/retire", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	wantLoc := fmt.Sprintf("/devices/%d", d.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	got, err := q.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice after retire: %v", err)
	}
	if got.RetiredAt == nil {
		t.Fatalf("RetiredAt = nil, want non-nil")
	}
}

func TestDevices_Retire_HidesFromDefaultList(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	r.ServeHTTP(httptest.NewRecorder(),
		handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/retire", d.ID), middleware.RoleLicenseManager, nil))

	ds, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(ds) != 0 {
		t.Errorf("expected no active devices after retire, got %d", len(ds))
	}
}

func TestDevices_Retire_NotFound_404(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/devices/9999/retire", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestDevices_Retire_AlreadyRetired_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	r.ServeHTTP(httptest.NewRecorder(),
		handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/retire", d.ID), middleware.RoleLicenseManager, nil))

	req2 := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/retire", d.ID), middleware.RoleLicenseManager, nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec2.Code, rec2.Body.String())
	}
	handlertest.AssertContains(t, rec2, "既に退役")
}

func TestDevices_Retire_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/retire", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_Restore_ClearsRetiredAt(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, derr := q.SoftDeleteDevice(context.Background(), d.ID); derr != nil {
		t.Fatalf("soft delete: %v", derr)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/restore", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetDevice(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("GetDevice after restore: %v", err)
	}
	if got.RetiredAt != nil {
		t.Errorf("RetiredAt = %v, want nil", got.RetiredAt)
	}
}

func TestDevices_Restore_AlreadyActive_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/restore", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "既に稼働")
}

func TestDevices_Restore_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode: "PC-001",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/devices/%d/restore", d.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestDevices_EditForm_PinsRetiredUser(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	user, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:     "PC-001",
		PrimaryUserID: &user.ID,
	})
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), user.ID); err != nil {
		t.Fatalf("soft delete user: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d/edit", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "(退職)")
	handlertest.AssertContains(t, rec, fmt.Sprintf(`value="%d" selected`, user.ID))
}

func TestDevices_EditForm_PinsInactiveDepartment(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT-GONE",
		Name: "旧資材部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	d, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:    "PC-001",
		DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := q.SoftDeleteDepartment(context.Background(), dept.ID); err != nil {
		t.Fatalf("soft delete dept: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/devices/%d/edit", d.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "(〜")
	handlertest.AssertContains(t, rec, fmt.Sprintf(`value="%d" selected`, dept.ID))
}

func TestDevices_List_RetiredUserLabel(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	user, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:     "PC-001",
		PrimaryUserID: &user.ID,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), user.ID); err != nil {
		t.Fatalf("soft delete user: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "田川太郎 (退職)")
}

func TestDevices_List_RetiredDepartmentLabel(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT-GONE",
		Name: "旧資材部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	if _, err := q.CreateDevice(context.Background(), repository.CreateDeviceParams{
		AssetCode:    "PC-001",
		DepartmentID: &dept.ID,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if _, err := q.SoftDeleteDepartment(context.Background(), dept.ID); err != nil {
		t.Fatalf("soft delete dept: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/devices", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "旧資材部 (〜")
}
