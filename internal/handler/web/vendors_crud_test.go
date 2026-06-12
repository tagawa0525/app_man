package web_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// strReadCloser は string をそのまま http.Request.Body にできるラッパ。
func strReadCloser(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

func TestVendors_Create_RedirectsToShow(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/vendors", middleware.RoleLicenseManager, url.Values{
		"name": {"Acme"},
		"url":  {"https://acme.example.com"},
		"note": {"検証用"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 1 || vs[0].Name != "Acme" {
		t.Fatalf("after create, vendors = %#v", vs)
	}
	wantLoc := fmt.Sprintf("/vendors/%d", vs[0].ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
}

func TestVendors_Create_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/vendors", middleware.RoleLicenseManager, url.Values{
		"name": {""},
		"url":  {"https://x.example.com"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	handlertest.AssertContains(t, rec, "名前は必須")

	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("vendors should not be created, got %d", len(vs))
	}
}

func TestVendors_Create_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	if _, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Adobe"}); err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, "/vendors", middleware.RoleLicenseManager, url.Values{
		"name": {"Adobe"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "同じ名前")
}

func TestVendors_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/vendors", middleware.RoleGeneralUser, url.Values{"name": {"Acme"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestVendors_Show_ListsChildProducts(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}
	if _, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Acrobat Pro DC",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	}); err != nil {
		t.Fatalf("seed CreateProduct: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/vendors/%d", v.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Adobe")
	handlertest.AssertContains(t, rec, "Acrobat Pro DC")
}

func TestVendors_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/vendors/9999", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

func TestVendors_Update_RewritesFields(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/vendors/%d", v.ID), middleware.RoleLicenseManager, url.Values{
		"name": {"Adobe Inc."},
		"url":  {"https://adobe.com"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	updated, err := q.GetVendor(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if updated.Name != "Adobe Inc." {
		t.Errorf("name = %q, want %q", updated.Name, "Adobe Inc.")
	}
	if updated.Url == nil || *updated.Url != "https://adobe.com" {
		t.Errorf("url = %v, want https://adobe.com", updated.Url)
	}
}

func TestVendors_Delete_RemovesRow(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Acme"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/vendors/%d/delete", v.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("expected no vendors after delete, got %d", len(vs))
	}
}

func TestVendors_Delete_BlockedByChildProduct_409(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}
	if _, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Acrobat",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	}); err != nil {
		t.Fatalf("seed CreateProduct: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/vendors/%d/delete", v.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "配下に製品")

	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 1 {
		t.Errorf("vendor should remain, got %d", len(vs))
	}
}

func TestVendors_CSRFTokenMissing_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	// PostForm を使わず直接組み立てて _csrf を含めない。
	body := url.Values{"name": {"NoCSRF"}}.Encode()
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodPost, "/vendors", middleware.RoleLicenseManager, nil)
	req.Body = http.NoBody
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(body))
	req.Body = strReadCloser(body)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}
