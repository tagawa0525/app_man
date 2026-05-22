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

func TestUsers_Create_RedirectsToShow(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	us, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(us) != 1 || us[0].EmployeeCode != "E001" || us[0].Name != "田川太郎" {
		t.Fatalf("after create, users = %#v", us)
	}
	if us[0].Source != "manual" {
		t.Errorf("source = %q, want manual", us[0].Source)
	}
	wantLoc := fmt.Sprintf("/users/%d", us[0].ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
}

func TestUsers_Create_StoresOptionalFields(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
		"username":      {"tagawa"},
		"email":         {"tagawa@example.com"},
		"department_id": {strconv.FormatInt(dept.ID, 10)},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	us, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(us) != 1 {
		t.Fatalf("got %d users, want 1", len(us))
	}
	u := us[0]
	if u.Username == nil || *u.Username != "tagawa" {
		t.Errorf("Username = %v, want 'tagawa'", u.Username)
	}
	if u.Email == nil || *u.Email != "tagawa@example.com" {
		t.Errorf("Email = %v, want 'tagawa@example.com'", u.Email)
	}
	if u.DepartmentID == nil || *u.DepartmentID != dept.ID {
		t.Errorf("DepartmentID = %v, want %d", u.DepartmentID, dept.ID)
	}
}

func TestUsers_Create_RejectsEmptyEmployeeCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {""},
		"name":          {"田川太郎"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "従業員コード")

	us, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(us) != 0 {
		t.Errorf("users should not be created, got %d", len(us))
	}
}

func TestUsers_Create_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {""},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	handlertest.AssertContains(t, rec, "氏名")
}

func TestUsers_Create_RejectsInvalidEmployeeCodeFormat(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E 001"}, // 空白は許可されない
		"name":          {"田川太郎"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "従業員コード")
}

func TestUsers_Create_RejectsInvalidEmail(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
		"email":         {"not-an-email"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "メール")
}

func TestUsers_Create_RejectsDuplicateEmployeeCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	}); err != nil {
		t.Fatalf("seed CreateUser: %v", err)
	}

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"別人"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "従業員コードが重複")
}

func TestUsers_Create_RejectsNonExistentDepartmentID(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
		"department_id": {"9999"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "指定された部署は存在しません")
}

func TestUsers_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users", middleware.RoleGeneralUser, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_Show_RendersDetail(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/users/%d", u.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	handlertest.AssertContains(t, rec, "田川太郎")
}

func TestUsers_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/users/9999", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestUsers_Show_LinksToDepartment(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	dept, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "営業本部",
	})
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
		DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/users/%d", u.ID), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, fmt.Sprintf(`href="/departments/%d"`, dept.ID))
	handlertest.AssertContains(t, rec, "営業本部")
}

func TestUsers_EditForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/users/%d/edit", u.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `value="E001"`)
	handlertest.AssertContains(t, rec, `value="田川太郎"`)
}

func TestUsers_EditForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/users/%d/edit", u.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_Update_RewritesFields(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d", u.ID), middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川次郎"},
		"email":         {"jiro@example.com"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	updated, err := q.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if updated.Name != "田川次郎" {
		t.Errorf("Name = %q, want %q", updated.Name, "田川次郎")
	}
	if updated.Email == nil || *updated.Email != "jiro@example.com" {
		t.Errorf("Email = %v, want 'jiro@example.com'", updated.Email)
	}
}

func TestUsers_Update_ClearsOptionalFieldsToNull(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	username := "tagawa"
	email := "tagawa@example.com"
	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
		Username:     &username,
		Email:        &email,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d", u.ID), middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"田川太郎"},
		"username":      {""},
		"email":         {""},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	updated, err := q.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if updated.Username != nil {
		t.Errorf("Username = %v, want nil", updated.Username)
	}
	if updated.Email != nil {
		t.Errorf("Email = %v, want nil", updated.Email)
	}
}

func TestUsers_Delete_SetsDeactivatedAt(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d/delete", u.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	wantLoc := fmt.Sprintf("/users/%d", u.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	got, err := q.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUser after delete: %v", err)
	}
	if got.DeactivatedAt == nil {
		t.Fatalf("DeactivatedAt = nil, want non-nil")
	}
}

func TestUsers_Delete_HidesFromDefaultList(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	r.ServeHTTP(httptest.NewRecorder(),
		handlertest.PostForm(t, fmt.Sprintf("/users/%d/delete", u.ID), middleware.RoleLicenseManager, nil))

	us, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(us) != 0 {
		t.Errorf("expected no active users after soft-delete, got %d", len(us))
	}
}

func TestUsers_Delete_NotFound_404(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/users/9999/delete", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestUsers_Delete_AlreadyDeactivated_409(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	r.ServeHTTP(httptest.NewRecorder(),
		handlertest.PostForm(t, fmt.Sprintf("/users/%d/delete", u.ID), middleware.RoleLicenseManager, nil))

	req2 := handlertest.PostForm(t, fmt.Sprintf("/users/%d/delete", u.ID), middleware.RoleLicenseManager, nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec2.Code, rec2.Body.String())
	}
	handlertest.AssertContains(t, rec2, "既に退職")
}

func TestUsers_Delete_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d/delete", u.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_Restore_ClearsDeactivatedAt(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, derr := q.SoftDeleteUser(context.Background(), u.ID); derr != nil {
		t.Fatalf("soft delete: %v", derr)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d/restore", u.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	got, err := q.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUser after restore: %v", err)
	}
	if got.DeactivatedAt != nil {
		t.Errorf("DeactivatedAt = %v, want nil", got.DeactivatedAt)
	}
}

func TestUsers_Restore_AlreadyActive_409(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d/restore", u.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "既に在籍")
}

func TestUsers_Restore_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d/restore", u.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_Update_RejectsDuplicateEmployeeCode(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	}); err != nil {
		t.Fatalf("seed1: %v", err)
	}
	u2, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "山田花子",
	})
	if err != nil {
		t.Fatalf("seed2: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/users/%d", u2.ID), middleware.RoleLicenseManager, url.Values{
		"employee_code": {"E001"},
		"name":          {"山田花子"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "従業員コードが重複")
}
