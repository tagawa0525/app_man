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

func TestProducts_Create_RedirectsAndPersists(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")

	req := handlertest.PostForm(t, "/products", middleware.RoleLicenseManager, url.Values{
		"vendor_id":               {fmt.Sprintf("%d", v.ID)},
		"canonical_name":          {"Acrobat Pro DC"},
		"edition":                 {"Pro"},
		"software_type":           {"installed"},
		"license_required":        {"true"},
		"default_approval_status": {"unknown"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	rows, err := q.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(rows) != 1 || rows[0].CanonicalName != "Acrobat Pro DC" {
		t.Fatalf("unexpected products: %#v", rows)
	}
	got, err := q.GetProduct(context.Background(), rows[0].ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got.SoftwareType != "installed" {
		t.Errorf("software_type = %q, want installed", got.SoftwareType)
	}
	if got.LicenseRequired == nil || !*got.LicenseRequired {
		t.Errorf("license_required = %v, want true", got.LicenseRequired)
	}
}

func TestProducts_Create_RejectsMissingVendor(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	req := handlertest.PostForm(t, "/products", middleware.RoleLicenseManager, url.Values{
		"canonical_name": {"Foo"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "ベンダー")
}

func TestProducts_Create_RejectsInvalidSoftwareType(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")

	req := handlertest.PostForm(t, "/products", middleware.RoleLicenseManager, url.Values{
		"vendor_id":      {fmt.Sprintf("%d", v.ID)},
		"canonical_name": {"Foo"},
		"software_type":  {"cloud"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "不正な種別")
}

func TestProducts_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")

	req := handlertest.PostForm(t, "/products", middleware.RoleGeneralUser, url.Values{
		"vendor_id":      {fmt.Sprintf("%d", v.ID)},
		"canonical_name": {"Foo"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestProducts_Update_RewritesFields(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d", p.ID), middleware.RoleLicenseManager, url.Values{
		"vendor_id":               {fmt.Sprintf("%d", v.ID)},
		"canonical_name":          {"Acrobat Pro DC"},
		"software_type":           {"installed"},
		"default_approval_status": {"globally_approved"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	got, err := q.GetProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got.CanonicalName != "Acrobat Pro DC" {
		t.Errorf("canonical_name = %q, want Acrobat Pro DC", got.CanonicalName)
	}
	if got.DefaultApprovalStatus != "globally_approved" {
		t.Errorf("default_approval_status = %q, want globally_approved", got.DefaultApprovalStatus)
	}
}

func TestProducts_Delete_RemovesRow(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d/delete", p.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	rows, err := q.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no products, got %d", len(rows))
	}
}

func TestProducts_EditForm_PopulatesExistingValues(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	p, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Photoshop",
		SoftwareType:          "saas",
		DefaultApprovalStatus: "globally_approved",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	req := handlertest.NewRequest(t, http.MethodGet, fmt.Sprintf("/products/%d/edit", p.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `value="Photoshop"`)
}
