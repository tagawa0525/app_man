package bootstrap_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// licenseRefs は licenses / assignments テストで共有するマスタの seed 結果。
type licenseRefs struct {
	vendor    repository.Vendor
	office    repository.Product // edition NULL
	vsPro     repository.Product // edition "Professional"
	dept      repository.Department
	oldDept   repository.Department // 廃止済み (valid_to NOT NULL)
	otherDept repository.Department
}

// seedLicenseMaster は vendor / products / departments を投入する。
// licenses kind の前提となる「マスタ投入済み環境」を作る。
func seedLicenseMaster(t *testing.T, db *sql.DB) licenseRefs {
	t.Helper()
	ctx := context.Background()
	q := repository.New(db)

	v, err := q.CreateVendor(ctx, repository.CreateVendorParams{Name: "Microsoft"})
	if err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	office, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID: v.ID, CanonicalName: "Office",
		SoftwareType: "installed", DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("seed product office: %v", err)
	}
	ed := "Professional"
	vsPro, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID: v.ID, CanonicalName: "Visual Studio", Edition: &ed,
		SoftwareType: "installed", DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("seed product vs: %v", err)
	}
	dept, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{Code: "D01", Name: "情報システム部"})
	if err != nil {
		t.Fatalf("seed department: %v", err)
	}
	otherDept, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{Code: "D02", Name: "総務部"})
	if err != nil {
		t.Fatalf("seed department D02: %v", err)
	}
	oldDept, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{Code: "D99", Name: "旧部署"})
	if err != nil {
		t.Fatalf("seed department D99: %v", err)
	}
	if _, err := q.SoftDeleteDepartment(ctx, oldDept.ID); err != nil {
		t.Fatalf("soft delete department D99: %v", err)
	}
	return licenseRefs{vendor: v, office: office, vsPro: vsPro, dept: dept, oldDept: oldDept, otherDept: otherDept}
}

const licensesHeader = "vendor_name,product_name,edition,department_code,license_slug,display_name,total_count,count_unit,contract_type,purchased_at,started_at,expires_at,vendor_order_no,purchaser,unit_price,currency,product_keys,note"

func TestLicensesImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedLicenseMaster(t, db)

	csv := writeCSV(t, licensesHeader+"\n"+
		"Microsoft,Office,,D01,o365-2026,Microsoft 365 E3,50,user,subscription,2026-04-01,2026-04-01,2027-03-31,PO-123,情シス,1500,JPY,KEY-AAA,備考\n"+
		"Microsoft,Visual Studio,Professional,D01,vs-pro,VS Pro 2022,,device,perpetual,,,,,,,,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	ctx := context.Background()
	q := repository.New(db)
	lic, err := q.GetLicenseByKey(ctx, repository.GetLicenseByKeyParams{
		ProductID: refs.office.ID, OwningDepartmentID: refs.dept.ID, LicenseSlug: "o365-2026",
	})
	if err != nil {
		t.Fatalf("GetLicenseByKey o365-2026: %v", err)
	}
	if lic.DisplayName != "Microsoft 365 E3" {
		t.Errorf("display_name = %q", lic.DisplayName)
	}
	if lic.TotalCount == nil || *lic.TotalCount != 50 {
		t.Errorf("total_count = %v, want 50", lic.TotalCount)
	}
	if lic.CountUnit != "user" || lic.ContractType != "subscription" {
		t.Errorf("count_unit/contract_type = %q/%q", lic.CountUnit, lic.ContractType)
	}
	if lic.ExpiresAt == nil || lic.ExpiresAt.Format("2006-01-02") != "2027-03-31" {
		t.Errorf("expires_at = %v, want 2027-03-31", lic.ExpiresAt)
	}
	if lic.ProductKeys == nil || *lic.ProductKeys != "KEY-AAA" {
		t.Errorf("product_keys = %v, want KEY-AAA", lic.ProductKeys)
	}
	if lic.FsDirPath != "licenses/Microsoft/Office/o365-2026" {
		t.Errorf("fs_dir_path = %q, want licenses/Microsoft/Office/o365-2026", lic.FsDirPath)
	}

	vs, err := q.GetLicenseByKey(ctx, repository.GetLicenseByKeyParams{
		ProductID: refs.vsPro.ID, OwningDepartmentID: refs.dept.ID, LicenseSlug: "vs-pro",
	})
	if err != nil {
		t.Fatalf("GetLicenseByKey vs-pro: %v", err)
	}
	if vs.TotalCount != nil {
		t.Errorf("total_count = %v, want nil (unlimited)", vs.TotalCount)
	}
	if vs.Currency == nil || *vs.Currency != "JPY" {
		t.Errorf("currency = %v, want default JPY", vs.Currency)
	}
	// スペースは slug 規則で _ に置換される
	if vs.FsDirPath != "licenses/Microsoft/Visual_Studio/vs-pro" {
		t.Errorf("fs_dir_path = %q, want licenses/Microsoft/Visual_Studio/vs-pro", vs.FsDirPath)
	}
}

func TestLicensesImporter_RejectsMissingRefs(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedLicenseMaster(t, db)

	csv := writeCSV(t, licensesHeader+"\n"+
		"NoSuchVendor,Office,,D01,s1,L1,,device,perpetual,,,,,,,,,\n"+
		"Microsoft,NoSuchProduct,,D01,s2,L2,,device,perpetual,,,,,,,,,\n"+
		"Microsoft,Office,,NOPE,s3,L3,,device,perpetual,,,,,,,,,\n"+
		"Microsoft,Office,,D99,s4,L4,,device,perpetual,,,,,,,,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column vendor_name: ベンダー 'NoSuchVendor' が見つかりません",
		"line 2, column product_name: 製品 'NoSuchProduct' が見つかりません",
		"line 3, column department_code: 部署 'NOPE' が見つかりません",
		"line 4, column department_code: 廃止済みの部署です",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestLicensesImporter_RejectsDuplicates(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedLicenseMaster(t, db)

	// DB 既存: 自然キー (office, D01, existing) を先に投入
	ctx := context.Background()
	q := repository.New(db)
	if _, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID: refs.office.ID, OwningDepartmentID: refs.dept.ID,
		LicenseSlug: "existing", DisplayName: "既存",
		CountUnit: "device", ContractType: "perpetual",
		FsDirPath: "licenses/Microsoft/Office/existing",
	}); err != nil {
		t.Fatalf("seed license: %v", err)
	}

	csv := writeCSV(t, licensesHeader+"\n"+
		// line 1: DB の自然キーと重複
		"Microsoft,Office,,D01,existing,L1,,device,perpetual,,,,,,,,,\n"+
		// line 2 & 3: CSV 内で自然キーが重複
		"Microsoft,Office,,D01,dup,L2,,device,perpetual,,,,,,,,,\n"+
		"Microsoft,Office,,D01,dup,L3,,device,perpetual,,,,,,,,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column license_slug: DB に既に登録されています",
		"line 3, column license_slug: CSV 内で重複しています (line 2)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestLicensesImporter_RejectsFsDirPathCollision(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedLicenseMaster(t, db)

	// DB 既存: fs_dir_path だけが衝突するライセンス (自然キーは D02 側)
	ctx := context.Background()
	q := repository.New(db)
	if _, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID: refs.office.ID, OwningDepartmentID: refs.otherDept.ID,
		LicenseSlug: "clash-db", DisplayName: "既存",
		CountUnit: "device", ContractType: "perpetual",
		FsDirPath: "licenses/Microsoft/Office/clash-db",
	}); err != nil {
		t.Fatalf("seed license: %v", err)
	}

	csv := writeCSV(t, licensesHeader+"\n"+
		// line 1: 部署が違うので自然キーは新規だが fs_dir_path は DB と衝突
		"Microsoft,Office,,D01,clash-db,L1,,device,perpetual,,,,,,,,,\n"+
		// line 2 & 3: 部署違いの 2 行が同じ fs_dir_path に落ちる (CSV 内衝突)
		"Microsoft,Office,,D01,clash-csv,L2,,device,perpetual,,,,,,,,,\n"+
		"Microsoft,Office,,D02,clash-csv,L3,,device,perpetual,,,,,,,,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column license_slug: fs_dir_path が DB の既存ライセンスと衝突します: licenses/Microsoft/Office/clash-db",
		"line 3, column license_slug: fs_dir_path が CSV 内で衝突します (line 2)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestLicensesImporter_RejectsInvalidValues(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedLicenseMaster(t, db)

	csv := writeCSV(t, licensesHeader+"\n"+
		// 必須欠落 (display_name) / 不正 enum / 不正数値 / 不正日付
		"Microsoft,Office,,D01,s1,,,seat,rental,2026/04/01,,,,,-100,,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column display_name: 表示名は必須です",
		"line 1, column count_unit: device / user のいずれかにしてください",
		"line 1, column contract_type: perpetual / subscription のいずれかにしてください",
		"line 1, column purchased_at: YYYY-MM-DD 形式で入力してください",
		"line 1, column unit_price: 0 以上の整数で入力してください",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestLicensesImporter_DryRun_DoesNotInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedLicenseMaster(t, db)

	csv := writeCSV(t, licensesHeader+"\n"+
		"Microsoft,Office,,D01,dry,L1,,device,perpetual,,,,,,,,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.LicensesImporter{}, true, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM licenses").Scan(&n); err != nil {
		t.Fatalf("count licenses: %v", err)
	}
	if n != 0 {
		t.Errorf("dry-run should not insert, got %d licenses", n)
	}
}
