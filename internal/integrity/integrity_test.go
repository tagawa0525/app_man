package integrity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/integrity"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// fixedNow は meta.yml 自動生成 (last_updated_by_app) の決定論検証用。
var fixedNow = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

// seedCatalog は vendor + product + 現役部署を 1 組投入する
// (licenses の FK 前提。generate-meta の runner_test と同流儀)。
func seedCatalog(t *testing.T, q *repository.Queries) (productID, deptID int64) {
	t.Helper()
	ctx := context.Background()
	v, err := q.CreateVendor(ctx, repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("CreateVendor: %v", err)
	}
	p, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Acrobat Pro",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	d, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "情報システム部",
	})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	return p.ID, d.ID
}

// seedLicense は fs_dir_path を指定してライセンスを 1 行投入する。
func seedLicense(t *testing.T, q *repository.Queries, productID, deptID int64, fsDirPath string) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        filepath.Base(fsDirPath),
		DisplayName:        "Acrobat 年間契約 " + filepath.Base(fsDirPath),
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          fsDirPath,
	})
	if err != nil {
		t.Fatalf("CreateLicense (%s): %v", fsDirPath, err)
	}
	return lic
}

// seedDocument は license_documents に 1 行投入する (物理ファイルは置かない)。
func seedDocument(t *testing.T, q *repository.Queries, licenseID int64, storedPath, sha string) repository.LicenseDocument {
	t.Helper()
	doc, err := q.CreateLicenseDocument(context.Background(), repository.CreateLicenseDocumentParams{
		LicenseID:        licenseID,
		DocType:          "certificate",
		StoredPath:       storedPath,
		OriginalFilename: filepath.Base(storedPath),
		Sha256:           sha,
	})
	if err != nil {
		t.Fatalf("CreateLicenseDocument (%s): %v", storedPath, err)
	}
	return doc
}

// placeFile は basePath 相対 (/ 区切り) の位置にファイルを書き、sha256 を返す。
func placeFile(t *testing.T, basePath, rel string, data []byte) string {
	t.Helper()
	abs := filepath.Join(basePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// newScanEnv は in-memory DB + 空 basePath (t.TempDir) を用意する。
func newScanEnv(t *testing.T) (*sql.DB, *repository.Queries, string) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)
	return sqlDB, repository.New(sqlDB), t.TempDir()
}

// mustScan は Scan の失敗しない前提版。
func mustScan(t *testing.T, q *repository.Queries, basePath string, dryRun bool) integrity.Report {
	t.Helper()
	rep, err := integrity.Scan(context.Background(), q, basePath, dryRun, fixedNow)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return rep
}

// requireSingleFinding は所見が 1 件で kind / license_id / path が一致する
// ことを検証する。
func requireSingleFinding(t *testing.T, rep integrity.Report, kind string, licenseID int64, path string) {
	t.Helper()
	if len(rep.Findings) != 1 {
		t.Fatalf("findings: want 1, got %d: %+v", len(rep.Findings), rep.Findings)
	}
	f := rep.Findings[0]
	if f.Kind != kind {
		t.Errorf("kind: want %q, got %q", kind, f.Kind)
	}
	if f.LicenseID != licenseID {
		t.Errorf("license_id: want %d, got %d", licenseID, f.LicenseID)
	}
	if f.Path != path {
		t.Errorf("path: want %q, got %q", path, f.Path)
	}
}

// TestScan_CleanState は DB と FS が整合した状態 (ディレクトリ・meta.yml・
// 証書ファイルすべて存在、sha256 一致) で所見 0 件・meta 生成 0 件になる
// ことを確認する。
func TestScan_CleanState(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newScanEnv(t)
	_ = sqlDB
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")

	docBytes := []byte("%PDF-1.7 clean certificate")
	sha := placeFile(t, basePath, lic.FsDirPath+"/invoice.pdf", docBytes)
	seedDocument(t, q, lic.ID, lic.FsDirPath+"/invoice.pdf", sha)
	if err := licensefs.Regenerate(context.Background(), q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate (pre-create meta): %v", err)
	}

	rep := mustScan(t, q, basePath, false)

	if len(rep.Findings) != 0 {
		t.Errorf("clean state must yield no findings, got: %+v", rep.Findings)
	}
	if rep.MetaGenerated != 0 || rep.WouldGenerateMeta != 0 {
		t.Errorf("clean state must not (would-)generate meta: %+v", rep)
	}

	// Scan は証書ファイルを変更しない。
	got, err := os.ReadFile(filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath), "invoice.pdf"))
	if err != nil {
		t.Fatalf("document must survive scan: %v", err)
	}
	if !bytes.Equal(got, docBytes) {
		t.Error("document bytes must be unchanged by Scan")
	}
}

// TestScan_DetectsFileMissing は stored_path の実体が無い証書行が
// file_missing として検出されることを確認する。
func TestScan_DetectsFileMissing(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")
	if err := licensefs.Regenerate(context.Background(), q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	storedPath := lic.FsDirPath + "/lost.pdf"
	seedDocument(t, q, lic.ID, storedPath, "deadbeef")

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "file_missing", lic.ID, storedPath)
}

// TestScan_DetectsSha256Mismatch はファイル内容が DB 記録の sha256 と
// 一致しない証書が sha256_mismatch として検出され、かつ Scan がファイルを
// 書き換えないことを確認する。
func TestScan_DetectsSha256Mismatch(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")

	storedPath := lic.FsDirPath + "/tampered.pdf"
	tampered := []byte("%PDF-1.7 tampered bytes")
	placeFile(t, basePath, storedPath, tampered)
	origSum := sha256.Sum256([]byte("%PDF-1.7 original bytes"))
	seedDocument(t, q, lic.ID, storedPath, hex.EncodeToString(origSum[:]))
	if err := licensefs.Regenerate(context.Background(), q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "sha256_mismatch", lic.ID, storedPath)

	got, err := os.ReadFile(filepath.Join(basePath, filepath.FromSlash(storedPath)))
	if err != nil {
		t.Fatalf("document must survive scan: %v", err)
	}
	if !bytes.Equal(got, tampered) {
		t.Error("Scan must not modify the document file")
	}
}

// TestScan_DetectsUnregisteredFile は契約フォルダ内にあるが DB に登録の
// 無いファイル (meta.yml 以外) が unregistered_file として検出されることを
// 確認する。
func TestScan_DetectsUnregisteredFile(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")
	if err := licensefs.Regenerate(context.Background(), q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	strayPath := lic.FsDirPath + "/stray-note.pdf"
	placeFile(t, basePath, strayPath, []byte("%PDF-1.7 placed by hand"))

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "unregistered_file", lic.ID, strayPath)
}

// TestScan_DetectsOrphanDir は licenses/ 配下の末端 (深さ 3) ディレクトリで
// どのライセンスの fs_dir_path にも対応しないものが orphan_dir として検出
// されることを確認する。license_id は無い (0)。
func TestScan_DetectsOrphanDir(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")
	if err := licensefs.Regenerate(context.Background(), q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	orphan := "licenses/ghost-vendor/ghost-product/2020-old"
	if err := os.MkdirAll(filepath.Join(basePath, filepath.FromSlash(orphan)), 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "orphan_dir", 0, orphan)
}

// TestScan_DetectsDirMissing は fs_dir_path の物理ディレクトリが無い
// ライセンスが dir_missing として検出され、meta.yml 生成は行われない
// (ディレクトリ復元は generate-meta の責務) ことを確認する。
func TestScan_DetectsDirMissing(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "dir_missing", lic.ID, lic.FsDirPath)
	if rep.MetaGenerated != 0 {
		t.Errorf("dir_missing row must not trigger meta generation, got %d", rep.MetaGenerated)
	}
	if _, err := os.Stat(filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath))); !os.IsNotExist(err) {
		t.Errorf("Scan must not create the missing directory, stat err = %v", err)
	}
}

// TestScan_DetectsInvalidPath は basePath を脱出する汚染 fs_dir_path が
// invalid_path として検出され、行の以降の検査がスキップされることを確認する。
func TestScan_DetectsInvalidPath(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "../evil")

	rep := mustScan(t, q, basePath, false)
	requireSingleFinding(t, rep, "invalid_path", lic.ID, "../evil")

	// basePath の外に何も作られない。
	if _, err := os.Stat(filepath.Join(filepath.Dir(basePath), "evil")); !os.IsNotExist(err) {
		t.Errorf("nothing must exist outside basePath, stat err = %v", err)
	}
}

// TestScan_GeneratesMissingMeta は meta.yml 欠落 (唯一の自動修復対象) が
// 実行モードで自動生成され、所見にはならないことを確認する。
func TestScan_GeneratesMissingMeta(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")
	dirAbs := filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath))
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		t.Fatalf("mkdir license dir: %v", err)
	}

	rep := mustScan(t, q, basePath, false)

	if len(rep.Findings) != 0 {
		t.Errorf("missing meta.yml must not be a finding, got: %+v", rep.Findings)
	}
	if rep.MetaGenerated != 1 {
		t.Errorf("MetaGenerated: want 1, got %d", rep.MetaGenerated)
	}
	if rep.WouldGenerateMeta != 0 {
		t.Errorf("WouldGenerateMeta: want 0 in run mode, got %d", rep.WouldGenerateMeta)
	}
	data, err := os.ReadFile(filepath.Join(dirAbs, "meta.yml"))
	if err != nil {
		t.Fatalf("meta.yml must be generated: %v", err)
	}
	if !bytes.Contains(data, []byte("license_slug: "+lic.LicenseSlug)) {
		t.Errorf("generated meta.yml should contain license_slug, got:\n%s", data)
	}
}

// TestScan_DryRunDoesNotGenerateMeta は dry-run では meta.yml を生成せず
// WouldGenerateMeta として報告することを確認する。
func TestScan_DryRunDoesNotGenerateMeta(t *testing.T) {
	t.Parallel()

	_, q, basePath := newScanEnv(t)
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "licenses/adobe/acrobat-pro/2024-jouki")
	dirAbs := filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath))
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		t.Fatalf("mkdir license dir: %v", err)
	}

	rep := mustScan(t, q, basePath, true)

	if len(rep.Findings) != 0 {
		t.Errorf("missing meta.yml must not be a finding, got: %+v", rep.Findings)
	}
	if rep.MetaGenerated != 0 {
		t.Errorf("MetaGenerated: want 0 in dry-run, got %d", rep.MetaGenerated)
	}
	if rep.WouldGenerateMeta != 1 {
		t.Errorf("WouldGenerateMeta: want 1, got %d", rep.WouldGenerateMeta)
	}
	if _, err := os.Stat(filepath.Join(dirAbs, "meta.yml")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create meta.yml, stat err = %v", err)
	}
}
