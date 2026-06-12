package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// seedVendor は products テストで多用する vendor 投入ヘルパ。
func seedVendor(t *testing.T, q *repository.Queries, name string) repository.Vendor {
	t.Helper()
	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: name})
	if err != nil {
		t.Fatalf("CreateVendor: %v", err)
	}
	return v
}

func seedProduct(t *testing.T, q *repository.Queries, vendorID int64, name string) repository.Product {
	t.Helper()
	p, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID:              vendorID,
		CanonicalName:         name,
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	return p
}

func TestProducts_List_GeneralUser_200(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	seedProduct(t, q, v.ID, "Acrobat Pro DC")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/products", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Acrobat Pro DC")
	handlertest.AssertContains(t, rec, "Adobe")
}

func TestProducts_List_SearchMatchesAlias(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat Pro DC")
	if _, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
	// 検索に引っかからない別商品も入れる。
	seedProduct(t, q, v.ID, "Photoshop")

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/products?q=Acrobat+DC", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Acrobat Pro DC")
	if body := rec.Body.String(); contains(body, "Photoshop") {
		t.Errorf("search 'Acrobat DC' should not include Photoshop, body:\n%s", body)
	}
}

func TestProducts_NewForm_LicenseManager_200(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	seedVendor(t, q, "Adobe") // vendor select の選択肢用

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/products/new", middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="canonical_name"`)
	handlertest.AssertContains(t, rec, "Adobe")
}

func TestProducts_NewForm_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/products/new", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestProducts_Show_DisplaysProductAndAliases(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat Pro DC")
	if _, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, fmt.Sprintf("/products/%d", p.ID), middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "Acrobat Pro DC")
	handlertest.AssertContains(t, rec, "Adobe")
	handlertest.AssertContains(t, rec, "Acrobat DC")
}

func TestProducts_Show_404OnUnknownID(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet, "/products/9999", middleware.RoleGeneralUser, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}
