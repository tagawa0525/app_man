package web_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/repository"
)

// newWebRouter は本 PR の handler/web を実環境に近い形でマウントした
// chi.Router を返す。各テストはこれを叩いて HTTP → templ → DB の往復を
// 通しで検証する。
func newWebRouter(t *testing.T) (http.Handler, *repository.Queries) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)

	r := chi.NewRouter()
	r.Use(middleware.DummyAuthMiddleware)
	r.Use(middleware.CSRFMiddleware)
	web.RegisterRoutes(r, web.Deps{
		Logger:  slog.New(slog.DiscardHandler),
		DB:      sqlDB,
		DevMode: true,
	})
	return r, repository.New(sqlDB)
}

func TestVendors_List_GeneralUser_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ベンダー")
}

func TestVendors_List_ShowsExistingVendors(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	if _, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{
		Name: "Adobe",
	}); err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}
	if _, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{
		Name: "Microsoft",
	}); err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Adobe")
	handlertest.AssertContains(t, rec, "Microsoft")
}

func TestVendors_List_HidesNewButton_ForGeneralUser(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, `href="/vendors/new"`) {
		t.Errorf("general_user should not see /vendors/new link, but body contains it:\n%s", body)
	}
}

func TestVendors_List_ShowsNewButton_ForLicenseManager(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `href="/vendors/new"`)
}

func TestVendors_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="name"`)
}

func TestVendors_NewForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.NewRequest(t, http.MethodGet, "/vendors/new", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

// contains は strings.Contains の薄いラッパ (テスト中の意図を明示)。
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
