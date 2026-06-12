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
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleLicenseManager, url.Values{
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

func TestAliases_Create_404OnUnknownProduct(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/products/9999/aliases", middleware.RoleLicenseManager, url.Values{
		"alias_name": {"X"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)

	all, err := q.ListAliasesByProduct(context.Background(), 9999)
	if err != nil {
		t.Fatalf("ListAliasesByProduct: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("no alias should have been created, got %d", len(all))
	}
}

func TestAliases_Create_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")
	if _, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	}); err != nil {
		t.Fatalf("seed CreateAlias: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleLicenseManager, url.Values{
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
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/products/%d/aliases", p.ID), middleware.RoleGeneralUser, url.Values{
		"alias_name": {"Foo"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

// 別 product 配下の alias の ID を推測して /products/{他のID}/aliases/{aid}/delete
// に POST しても 404 になり削除されないこと。
func TestAliases_Delete_BlocksCrossProduct(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p1 := seedProduct(t, q, v.ID, "Acrobat")
	p2 := seedProduct(t, q, v.ID, "Photoshop")
	a, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p1.ID,
		AliasName: "Acrobat DC",
	})
	if err != nil {
		t.Fatalf("seed CreateAlias: %v", err)
	}

	// p2 の URL に p1 配下の alias ID を渡してみる。
	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/products/%d/aliases/%d/delete", p2.ID, a.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)

	aliases, err := q.ListAliasesByProduct(context.Background(), p1.ID)
	if err != nil {
		t.Fatalf("ListAliasesByProduct: %v", err)
	}
	if len(aliases) != 1 {
		t.Errorf("alias should remain on p1, got %d", len(aliases))
	}
}

func TestAliases_Delete_RemovesRow(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)
	v := seedVendor(t, q, "Adobe")
	p := seedProduct(t, q, v.ID, "Acrobat")
	a, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID,
		AliasName: "Acrobat DC",
	})
	if err != nil {
		t.Fatalf("seed CreateAlias: %v", err)
	}

	req := handlertest.AuthenticatedPostForm(t, db, store, fmt.Sprintf("/products/%d/aliases/%d/delete", p.ID, a.ID), middleware.RoleLicenseManager, nil)
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
