package bootstrap_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

func seedVendor(t *testing.T, db *repository.Queries, name string) repository.Vendor {
	t.Helper()
	v, err := db.CreateVendor(context.Background(), repository.CreateVendorParams{Name: name})
	if err != nil {
		t.Fatalf("seed vendor %q: %v", name, err)
	}
	return v
}

func TestProductsImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedVendor(t, q, "Microsoft")
	seedVendor(t, q, "Adobe")

	csv := writeCSV(t, `vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
Microsoft,Office,Standard,installed,true,approved,
Adobe,Photoshop,,installed,true,approved,creative
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	ps, err := q.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d products, want 2", len(ps))
	}
}

func TestProductsImporter_RejectsMissingVendor(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
NotExist,Office,Standard,installed,true,approved,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want missing vendor error")
	}
	if !strings.Contains(out.String(), "ベンダー 'NotExist' が見つかりません") {
		t.Errorf("out = %q, want missing vendor message", out.String())
	}
}

func TestProductsImporter_RejectsDuplicateInDB(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	v := seedVendor(t, q, "Microsoft")
	ed := "Standard"
	if _, err := q.CreateProduct(context.Background(), repository.CreateProductParams{
		VendorID: v.ID, CanonicalName: "Office", Edition: &ed,
		SoftwareType: "installed", DefaultApprovalStatus: "unknown",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	csv := writeCSV(t, `vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
Microsoft,Office,Standard,installed,true,approved,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want DB duplicate error")
	}
	if !strings.Contains(out.String(), "DB に既に登録") {
		t.Errorf("out = %q, want DB 既存重複", out.String())
	}
}

func TestProductsImporter_RejectsInvalidEnumValues(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedVendor(t, q, "Microsoft")

	csv := writeCSV(t, `vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
Microsoft,Office,Standard,unknown_type,maybe,bogus,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want enum validation errors")
	}
	body := out.String()
	for _, want := range []string{"software_type", "license_required", "default_approval_status"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in output:\n%s", want, body)
		}
	}
}

func TestProductsImporter_EmptyEditionTreatedAsNull(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedVendor(t, q, "Adobe")

	csv := writeCSV(t, `vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
Adobe,Photoshop,,installed,true,approved,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	ps, _ := q.ListProducts(context.Background())
	if len(ps) != 1 {
		t.Fatalf("got %d products, want 1", len(ps))
	}
	if ps[0].Edition != nil {
		t.Errorf("Edition = %v, want nil", *ps[0].Edition)
	}
}
