package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// fixedNow は last_updated_by_app の決定論検証用の固定時刻。
var fixedNow = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

// seedCatalog は vendor + product + 現役部署を 1 組投入する
// (licenses の FK 前提)。
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
// 物理ディレクトリは作らない (L-1〜L-3 期間の backfill 対象を再現)。
func seedLicense(t *testing.T, q *repository.Queries, productID, deptID int64, slug string) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        slug,
		DisplayName:        "Acrobat 年間契約 " + slug,
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          "licenses/adobe/acrobat-pro/" + slug,
	})
	if err != nil {
		t.Fatalf("CreateLicense (%s): %v", slug, err)
	}
	return lic
}

// newGenerateEnv は in-memory DB + 空 basePath (t.TempDir) を用意する。
func newGenerateEnv(t *testing.T) (*sql.DB, *repository.Queries, string) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)
	return sqlDB, repository.New(sqlDB), t.TempDir()
}

// mustDirAbs は licensefs.DirAbs の失敗しない前提版 (テスト内の正常パス用)。
func mustDirAbs(t *testing.T, basePath, fsDirPath string) string {
	t.Helper()
	p, err := licensefs.DirAbs(basePath, fsDirPath)
	if err != nil {
		t.Fatalf("DirAbs(%q): %v", fsDirPath, err)
	}
	return p
}

// readMeta は license の meta.yml を読む。存在しなければ Fatal。
func readMeta(t *testing.T, basePath, fsDirPath string) []byte {
	t.Helper()
	p := filepath.Join(mustDirAbs(t, basePath, fsDirPath), "meta.yml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read meta.yml %s: %v", p, err)
	}
	return data
}

// TestGenerateAll_BackfillCreatesDirAndMeta は「fs_dir_path はあるが物理
// ディレクトリ / meta.yml が無い」2 ライセンスに対して、両方のディレクトリと
// meta.yml が生成されること (backfill) を確認する。
func TestGenerateAll_BackfillCreatesDirAndMeta(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	licA := seedLicense(t, q, productID, deptID, "2024-jouki")
	licB := seedLicense(t, q, productID, deptID, "2024-kaki")

	if err := generateAll(ctx, sqlDB, basePath, slog.New(slog.DiscardHandler), fixedNow, false); err != nil {
		t.Fatalf("generateAll: %v", err)
	}

	for _, lic := range []repository.License{licA, licB} {
		data := readMeta(t, basePath, lic.FsDirPath)
		if !strings.Contains(string(data), "license_slug: "+lic.LicenseSlug) {
			t.Errorf("meta.yml for %s should contain its license_slug, got:\n%s", lic.LicenseSlug, data)
		}
		// now 注入の決定論: fixedNow (UTC 12:00) は JST 21:00 で出力される。
		if !strings.Contains(string(data), "last_updated_by_app: 2026-07-04T21:00:00+09:00") {
			t.Errorf("meta.yml for %s should stamp injected now, got:\n%s", lic.LicenseSlug, data)
		}
	}
}

// TestGenerateAll_RestoresBrokenMeta は手動編集で壊れた meta.yml が
// 再実行で元の内容に復元されることを確認する。
func TestGenerateAll_RestoresBrokenMeta(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "2024-jouki")

	if err := generateAll(ctx, sqlDB, basePath, slog.New(slog.DiscardHandler), fixedNow, false); err != nil {
		t.Fatalf("generateAll (initial): %v", err)
	}
	want := readMeta(t, basePath, lic.FsDirPath)

	metaPath := filepath.Join(mustDirAbs(t, basePath, lic.FsDirPath), "meta.yml")
	if err := os.WriteFile(metaPath, []byte("broken: [\n"), 0o644); err != nil {
		t.Fatalf("break meta.yml: %v", err)
	}

	if err := generateAll(ctx, sqlDB, basePath, slog.New(slog.DiscardHandler), fixedNow, false); err != nil {
		t.Fatalf("generateAll (restore): %v", err)
	}
	if got := readMeta(t, basePath, lic.FsDirPath); !bytes.Equal(got, want) {
		t.Errorf("meta.yml should be restored byte-identically.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestGenerateAll_DoesNotTouchDocuments は契約フォルダ内の証書ファイルが
// 再生成で書き換えられない (バイト列不変) ことを確認する。
func TestGenerateAll_DoesNotTouchDocuments(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "2024-jouki")

	dirAbs := mustDirAbs(t, basePath, lic.FsDirPath)
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		t.Fatalf("mkdir license dir: %v", err)
	}
	docPath := filepath.Join(dirAbs, "20240401-invoice.pdf")
	docBytes := []byte("%PDF-1.7 dummy certificate bytes")
	if err := os.WriteFile(docPath, docBytes, 0o644); err != nil {
		t.Fatalf("place dummy document: %v", err)
	}

	if err := generateAll(ctx, sqlDB, basePath, slog.New(slog.DiscardHandler), fixedNow, false); err != nil {
		t.Fatalf("generateAll: %v", err)
	}

	got, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("document file must survive: %v", err)
	}
	if !bytes.Equal(got, docBytes) {
		t.Error("document file bytes must be unchanged")
	}
	readMeta(t, basePath, lic.FsDirPath) // meta.yml は生成される
}

// TestGenerateAll_DryRun は dry-run が FS に一切触れず、total と
// would_create (meta.yml が現存しない件数) をログに出すことを確認する。
func TestGenerateAll_DryRun(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	seedLicense(t, q, productID, deptID, "2024-jouki")
	seedLicense(t, q, productID, deptID, "2024-kaki")
	// 3 件目は meta.yml が現存する → would_create に数えない。
	licC := seedLicense(t, q, productID, deptID, "2024-touki")
	if err := licensefs.Regenerate(ctx, q, basePath, licC.ID, fixedNow); err != nil {
		t.Fatalf("pre-create meta for licC: %v", err)
	}
	existingMeta := readMeta(t, basePath, licC.FsDirPath)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := generateAll(ctx, sqlDB, basePath, logger, fixedNow.Add(time.Hour), true); err != nil {
		t.Fatalf("generateAll dry-run: %v", err)
	}

	var entry struct {
		Total       *int64 `json:"total"`
		WouldCreate *int64 `json:"would_create"`
	}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("parse dry-run log %q: %v", buf.String(), err)
	}
	if entry.Total == nil || *entry.Total != 3 {
		t.Errorf("total: want 3, got %v (log: %s)", entry.Total, buf.String())
	}
	if entry.WouldCreate == nil || *entry.WouldCreate != 2 {
		t.Errorf("would_create: want 2, got %v (log: %s)", entry.WouldCreate, buf.String())
	}

	// FS 無変更: 未作成の 2 件のディレクトリは作られず、既存 meta も不変。
	entries, err := os.ReadDir(filepath.Join(basePath, "licenses", "adobe", "acrobat-pro"))
	if err != nil {
		t.Fatalf("read product dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dry-run must not create dirs: want 1 entry (licC only), got %d", len(entries))
	}
	if got := readMeta(t, basePath, licC.FsDirPath); !bytes.Equal(got, existingMeta) {
		t.Error("dry-run must not rewrite existing meta.yml")
	}
}

// TestGenerateAll_DryRunReportsEscapingFsDirPath は汚染された fs_dir_path
// (basePath を脱出する行) が dry-run で黙って無視されず、failed として
// カウント・ログされ error が返る (exit 1 相当で運用者に見える) ことを
// 確認する。would_create は正常な未作成行のみを数える。
func TestGenerateAll_DryRunReportsEscapingFsDirPath(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	seedLicense(t, q, productID, deptID, "2024-jouki") // 正常な未作成行
	// 汚染行: fs_dir_path が basePath の外を指す。
	if _, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        "poisoned",
		DisplayName:        "汚染行",
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          "../evil",
	}); err != nil {
		t.Fatalf("CreateLicense (poisoned): %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	err := generateAll(ctx, sqlDB, basePath, logger, fixedNow, true)
	if err == nil {
		t.Fatal("dry-run with escaping fs_dir_path: want error, got nil")
	}

	// dry-run の集計行 (would_create を含む行) を探して検証する。
	var entry struct {
		Total       *int64 `json:"total"`
		WouldCreate *int64 `json:"would_create"`
		Failed      *int64 `json:"failed"`
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "would_create") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse dry-run log %q: %v", line, err)
		}
		found = true
	}
	if !found {
		t.Fatalf("dry-run summary log missing, got: %s", buf.String())
	}
	if entry.Total == nil || *entry.Total != 2 {
		t.Errorf("total: want 2, got %v (log: %s)", entry.Total, buf.String())
	}
	if entry.WouldCreate == nil || *entry.WouldCreate != 1 {
		t.Errorf("would_create: want 1 (clean row only), got %v (log: %s)", entry.WouldCreate, buf.String())
	}
	if entry.Failed == nil || *entry.Failed != 1 {
		t.Errorf("failed: want 1 (poisoned row), got %v (log: %s)", entry.Failed, buf.String())
	}

	// basePath の外 (親ディレクトリ) に evil が作られていない。
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(basePath), "evil")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("nothing must exist outside basePath, stat err = %v", statErr)
	}
}

// TestGenerateAll_PartialFailureContinues は 1 件の失敗 (fs_dir_path の
// 位置に同名の通常ファイルがあり MkdirAll が失敗) で中断せず、他の
// ライセンスは処理された上で error が返る (exit 1 相当) ことを確認する。
func TestGenerateAll_PartialFailureContinues(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newGenerateEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	licBroken := seedLicense(t, q, productID, deptID, "2024-jouki")
	licOK := seedLicense(t, q, productID, deptID, "2024-kaki")

	// licBroken の fs_dir_path の位置に同名の通常ファイルを置く →
	// MkdirAll が ENOTDIR で失敗する。
	brokenAbs := mustDirAbs(t, basePath, licBroken.FsDirPath)
	if err := os.MkdirAll(filepath.Dir(brokenAbs), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(brokenAbs, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("place blocking file: %v", err)
	}

	err := generateAll(ctx, sqlDB, basePath, slog.New(slog.DiscardHandler), fixedNow, false)
	if err == nil {
		t.Fatal("generateAll with one failing license: want error, got nil")
	}

	// 失敗した 1 件以外は処理されている。
	readMeta(t, basePath, licOK.FsDirPath)
	// 壊れた行の MetaExists は ENOTDIR を「存在しない」に倒さず error に
	// する (実行時に必ず失敗する行を dry-run でも failed として予告する)。
	if _, err := licensefs.MetaExists(basePath, licBroken.FsDirPath); err == nil {
		t.Error("MetaExists on a blocked fs_dir_path should return error, got nil")
	}
	if fi, statErr := os.Stat(brokenAbs); statErr != nil || !fi.Mode().IsRegular() {
		t.Error("blocking file must remain untouched")
	}
}
