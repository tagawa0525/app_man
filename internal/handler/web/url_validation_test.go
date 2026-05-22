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

// vendor の url 入力で javascript: スキームを保存できないこと。
// (XSS 防御 — 保存時に弾く)
func TestVendors_Create_RejectsJavaScriptURL(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/vendors", middleware.RoleLicenseManager, url.Values{
		"name": {"BadVendor"},
		"url":  {"javascript:alert(1)"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "http")

	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("vendor should not be created, got %d", len(vs))
	}
}

func TestVendors_Create_AcceptsHTTPSURL(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)

	req := handlertest.PostForm(t, "/vendors", middleware.RoleLicenseManager, url.Values{
		"name": {"Good"},
		"url":  {"https://example.com"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	vs, _ := q.ListVendors(context.Background())
	if len(vs) != 1 {
		t.Errorf("expected 1 vendor, got %d", len(vs))
	}
}

func TestProducts_Create_RejectsJavaScriptURL(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")

	req := handlertest.PostForm(t, "/products", middleware.RoleLicenseManager, url.Values{
		"vendor_id":              {fmt.Sprintf("%d", v.ID)},
		"canonical_name":         {"Bad"},
		"canonical_download_url": {"javascript:alert(1)"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "http")

	rows, err := q.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("product should not be created, got %d", len(rows))
	}
}

func TestProducts_Create_RejectsJavaScriptURL_InAllURLFields(t *testing.T) {
	t.Parallel()
	r, _ := newWebRouter(t)

	for _, field := range []string{"canonical_download_url", "service_admin_url", "license_terms_url"} {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			req := handlertest.PostForm(t, "/products", middleware.RoleLicenseManager, url.Values{
				"vendor_id":      {"1"},
				"canonical_name": {"Bad"},
				field:            {"javascript:alert(1)"},
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// vendor の url を javascript: にしたまま update もできないこと。
func TestVendors_Update_RejectsJavaScriptURL(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Vendor1"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/vendors/%d", v.ID), middleware.RoleLicenseManager, url.Values{
		"name": {"Vendor1"},
		"url":  {"javascript:alert(1)"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	updated, err := q.GetVendor(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if updated.Url != nil {
		t.Errorf("vendor url should remain nil, got %v", *updated.Url)
	}
}
