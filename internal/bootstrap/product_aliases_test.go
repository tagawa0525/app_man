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

func seedProduct(t *testing.T, q *repository.Queries, vendorName, canonical, edition string) repository.Product {
	t.Helper()
	v := seedVendor(t, q, vendorName)
	params := repository.CreateProductParams{
		VendorID: v.ID, CanonicalName: canonical,
		SoftwareType: "installed", DefaultApprovalStatus: "unknown",
	}
	if edition != "" {
		ed := edition
		params.Edition = &ed
	}
	p, err := q.CreateProduct(context.Background(), params)
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return p
}

func TestProductAliasesImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedProduct(t, q, "Microsoft", "Office", "Standard")
	seedProduct(t, q, "Adobe", "Photoshop", "")

	csv := writeCSV(t, `product_vendor_name,product_canonical_name,product_edition,alias_name
Microsoft,Office,Standard,MS Office Standard 2021
Adobe,Photoshop,,Adobe Photoshop CC
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductAliasesImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
}

func TestProductAliasesImporter_RejectsMissingProduct(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedVendor(t, q, "Microsoft")

	csv := writeCSV(t, `product_vendor_name,product_canonical_name,product_edition,alias_name
Microsoft,Office,Pro,MS Office Pro
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductAliasesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want missing product error")
	}
	if !strings.Contains(out.String(), "製品 'Office'") {
		t.Errorf("out = %q, want product not found", out.String())
	}
}

func TestProductAliasesImporter_RejectsDuplicateAliasInDB(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	p := seedProduct(t, q, "Microsoft", "Office", "Standard")
	if _, err := q.CreateAlias(context.Background(), repository.CreateAliasParams{
		ProductID: p.ID, AliasName: "MS Office Standard 2021",
	}); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	csv := writeCSV(t, `product_vendor_name,product_canonical_name,product_edition,alias_name
Microsoft,Office,Standard,MS Office Standard 2021
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductAliasesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want duplicate alias error")
	}
	if !strings.Contains(out.String(), "DB に既に登録") {
		t.Errorf("out = %q, want DB duplicate", out.String())
	}
}

func TestProductAliasesImporter_RejectsEmptyAlias(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	seedProduct(t, q, "Microsoft", "Office", "Standard")

	csv := writeCSV(t, `product_vendor_name,product_canonical_name,product_edition,alias_name
Microsoft,Office,Standard,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.ProductAliasesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want empty alias error")
	}
	if !strings.Contains(out.String(), "別名は必須です") {
		t.Errorf("out = %q, want empty alias message", out.String())
	}
}
