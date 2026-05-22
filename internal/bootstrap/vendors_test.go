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

func TestVendorsImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `name,url,note
Microsoft,https://www.microsoft.com/,
Adobe,https://www.adobe.com/,note for adobe
JetBrains,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, false, &out)
	if err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	q := repository.New(db)
	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("got %d vendors, want 3", len(vs))
	}
	names := map[string]bool{}
	for _, v := range vs {
		names[v.Name] = true
	}
	for _, want := range []string{"Microsoft", "Adobe", "JetBrains"} {
		if !names[want] {
			t.Errorf("missing vendor: %q", want)
		}
	}
}

func TestVendorsImporter_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `name,url,note
,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation error")
	}
	if !strings.Contains(out.String(), "名前は必須です") {
		t.Errorf("out = %q, want '名前は必須です'", out.String())
	}
}

func TestVendorsImporter_RejectsDuplicateInCSV(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `name,url,note
Microsoft,,
Microsoft,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want duplicate error")
	}
	if !strings.Contains(out.String(), "CSV 内で重複") {
		t.Errorf("out = %q, want CSV 内で重複", out.String())
	}
}

func TestVendorsImporter_RejectsExistingInDB(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	q := repository.New(db)
	if _, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Microsoft"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	csv := writeCSV(t, `name,url,note
Microsoft,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want existing in DB error")
	}
	if !strings.Contains(out.String(), "DB に既に登録") {
		t.Errorf("out = %q, want DB に既に登録", out.String())
	}
}

func TestVendorsImporter_RejectsInvalidURL(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `name,url,note
Evil,javascript:alert(1),
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want invalid URL error")
	}
	if !strings.Contains(out.String(), "URL は http") {
		t.Errorf("out = %q, want URL scheme error", out.String())
	}
}

func TestVendorsImporter_DryRun_DoesNotInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `name,url,note
Microsoft,,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.VendorsImporter{}, true, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	q := repository.New(db)
	vs, _ := q.ListVendors(context.Background())
	if len(vs) != 0 {
		t.Errorf("dry-run should not insert, got %d vendors", len(vs))
	}
}
