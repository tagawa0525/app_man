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

func TestAliases_Create_AppearsOnShow(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleLicenseManager, url.Values{
		"alias_name": {"Acrobat DC"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}

	aliases, err := q.ListAliasesByProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("ListAliasesByProduct: %v", err)
	}
	if len(aliases) != 1 || aliases[0].AliasName != "Acrobat DC" {
		t.Fatalf("unexpected aliases: %#v", aliases)
	}
	if aliases[0].Source != "manual" {
		t.Errorf("source = %q, want manual", aliases[0].Source)
	}
}

func TestAliases_Create_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")
	if _, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	}); err != nil {
		t.Fatalf("seed CreateAlias: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleLicenseManager, url.Values{
		"alias_name": {"Acrobat DC"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	handlertest.AssertContains(t, rec, "同じエイリアス")
}

func TestAliases_Create_GeneralUser_403(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleGeneralUser, url.Values{
		"alias_name": {"Foo"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestAliases_Delete_RemovesRow(t *testing.T) {
	t.Parallel()
	r, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")
	a, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	})
	if err != nil {
		t.Fatalf("seed CreateAlias: %v", err)
	}

	req := handlertest.PostForm(t, fmt.Sprintf("/products/%d/aliases/%d/delete", p.ID, a.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code < 300 || rec.Code >= 400 {
		t.Fatalf("status = %d, want 3xx (body: %s)", rec.Code, rec.Body.String())
	}
	aliases, err := q.ListAliasesByProduct(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("ListAliasesByProduct: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(aliases))
	}
}
