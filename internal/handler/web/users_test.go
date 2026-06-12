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

// /users は要件書 §6.1 で「viewer 以上」と規定されており、
// general_user は閲覧できない (departments と同じ閾値、vendors / products
// より厳しい)。departmentViewers をそのまま流用するため、テストは
// departments_test.go と対称の構成で並べる。

func TestUsers_List_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_List_Viewer_200(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ユーザ")
}

func TestUsers_List_ShowsExistingUsers(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	}); err != nil {
		t.Fatalf("seed CreateUser: %v", err)
	}
	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "山田花子",
	}); err != nil {
		t.Fatalf("seed CreateUser: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	handlertest.AssertContains(t, rec, "田川太郎")
	handlertest.AssertContains(t, rec, "E002")
	handlertest.AssertContains(t, rec, "山田花子")
}

func TestUsers_List_HidesNewButton_ForViewer(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, `href="/users/new"`) {
		t.Errorf("viewer should not see /users/new link, body=\n%s", body)
	}
}

func TestUsers_List_ShowsNewButton_ForLicenseManager(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `href="/users/new"`)
}

func TestUsers_NewForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users/new", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestUsers_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="employee_code"`)
	handlertest.AssertContains(t, rec, `name="name"`)
	handlertest.AssertContains(t, rec, `name="username"`)
	handlertest.AssertContains(t, rec, `name="email"`)
	handlertest.AssertContains(t, rec, `name="department_id"`)
}

func TestUsers_Search_MatchesEmployeeCode(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "X999",
		Name:         "別人",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users?q=E001", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	if body := rec.Body.String(); contains(body, "X999") {
		t.Errorf("search E001 should not match X999, body=\n%s", body)
	}
}

func TestUsers_List_ExcludesInactive_ByDefault(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "現役社員",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "退職者A",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	if body := rec.Body.String(); contains(body, "E002") {
		t.Errorf("default list should not show retired user, body=\n%s", body)
	}
}

func TestUsers_List_IncludesInactive_WithQueryParam(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "現役社員",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "退職者A",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users?include_inactive=1", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	handlertest.AssertContains(t, rec, "E002")
	handlertest.AssertContains(t, rec, "退職")
	handlertest.AssertContains(t, rec, "row-inactive")
}

func TestUsers_List_IncludeInactiveCheckbox_Rendered(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="include_inactive"`)
}

func TestUsers_Search_ExcludesInactive_ByDefault(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "現役社員",
	}); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	retired, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "退職者E",
	})
	if err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users?q=E0", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	if body := rec.Body.String(); contains(body, "E002") {
		t.Errorf("default search should not show retired user, body=\n%s", body)
	}
}

func TestUsers_Search_IncludesInactive_WithQueryParam(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	retired, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "退職者E",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), retired.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users?q=E002&include_inactive=1", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E002")
}

func TestUsers_Show_RetiredBadgeShown(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.SoftDeleteUser(context.Background(), u.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users/"+strconv.FormatInt(u.ID, 10), middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "退職")
}

func TestUsers_Search_MatchesEmail(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	email1 := "tagawa@example.com"
	email2 := "other@example.com"
	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E001",
		Name:         "田川太郎",
		Email:        &email1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: "E002",
		Name:         "別人",
		Email:        &email2,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/users?q=tagawa", middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "E001")
	if body := rec.Body.String(); contains(body, "E002") {
		t.Errorf("search tagawa should not match E002, body=\n%s", body)
	}
}
