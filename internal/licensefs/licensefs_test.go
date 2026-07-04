package licensefs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// newBase は親ディレクトリ付きの basePath を作る (脱出検証で「base の外に
// 何も作られていない」ことを親ディレクトリの中身で確認するため)。
func newBase(t *testing.T) (base, parent string) {
	t.Helper()
	parent = t.TempDir()
	base = filepath.Join(parent, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	return base, parent
}

func TestDirAbs_Valid(t *testing.T) {
	t.Parallel()
	base, _ := newBase(t)

	got, err := licensefs.DirAbs(base, "licenses/adobe/acrobat-pro/2024-jouki")
	if err != nil {
		t.Fatalf("DirAbs: %v", err)
	}
	want := filepath.Join(base, "licenses", "adobe", "acrobat-pro", "2024-jouki")
	if got != want {
		t.Errorf("DirAbs = %q, want %q", got, want)
	}
}

// TestDirAbs_RejectsBaseEscape は汚染された fs_dir_path (DB 由来だが多層
// 防御。filestore.Store.Open と同じ思想) が basePath の外を指す場合に
// エラーになることを確認する。
func TestDirAbs_RejectsBaseEscape(t *testing.T) {
	t.Parallel()
	base, _ := newBase(t)

	cases := []struct {
		name      string
		fsDirPath string
	}{
		{"absolute", base}, // 絶対パス (t.TempDir 由来なので両 OS で絶対)
		{"parent", "../evil"},
		{"cleaned-parent", "a/../../evil"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := licensefs.DirAbs(base, tc.fsDirPath); err == nil {
				t.Errorf("DirAbs(%q) should reject base escape, got nil error", tc.fsDirPath)
			}
		})
	}
}

func TestMetaExists_ReportsExistence(t *testing.T) {
	t.Parallel()
	base, _ := newBase(t)
	const dir = "licenses/v/p/l"

	exists, err := licensefs.MetaExists(base, dir)
	if err != nil {
		t.Fatalf("MetaExists (missing): %v", err)
	}
	if exists {
		t.Error("MetaExists should be false before meta.yml is written")
	}

	abs := filepath.Join(base, filepath.FromSlash(dir))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(abs, "meta.yml"), []byte("product: x\n"), 0o644); err != nil {
		t.Fatalf("write meta.yml: %v", err)
	}

	exists, err = licensefs.MetaExists(base, dir)
	if err != nil {
		t.Fatalf("MetaExists (present): %v", err)
	}
	if !exists {
		t.Error("MetaExists should be true after meta.yml is written")
	}
}

// TestMetaExists_PropagatesStatFailures は ENOENT 以外の stat エラーや
// meta.yml が通常ファイルでないケースを「存在しない」と誤答せず error に
// することを確認する (dry-run の would_create 誤集計防止)。
func TestMetaExists_PropagatesStatFailures(t *testing.T) {
	t.Parallel()
	base, _ := newBase(t)

	// meta.yml がディレクトリ → error (通常ファイルではない)
	const dirMeta = "licenses/v/p/dirmeta"
	if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(dirMeta), "meta.yml"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := licensefs.MetaExists(base, dirMeta); err == nil {
		t.Error("MetaExists should return error when meta.yml is a directory")
	}

	// 親ディレクトリの権限で stat が EACCES → error (ENOENT ではない)
	if os.Getuid() == 0 {
		t.Skip("permission test is meaningless as root")
	}
	const denied = "licenses/v/p/denied"
	deniedAbs := filepath.Join(base, filepath.FromSlash(denied))
	if err := os.MkdirAll(deniedAbs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deniedAbs, "meta.yml"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(deniedAbs, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(deniedAbs, 0o755) })
	if _, err := licensefs.MetaExists(base, denied); err == nil {
		t.Error("MetaExists should return error on permission-denied stat")
	}
}

// TestMetaExists_RejectsBaseEscape は脱出する fs_dir_path が false では
// なくエラーになることを確認する (汚染行が「meta 無し = would_create」と
// して黙って集計されるのを防ぎ、呼び出し側が failed としてログできる)。
func TestMetaExists_RejectsBaseEscape(t *testing.T) {
	t.Parallel()
	base, _ := newBase(t)

	if _, err := licensefs.MetaExists(base, "../evil"); err == nil {
		t.Error("MetaExists with escaping fs_dir_path should return an error")
	}
}

// seedEscapingLicense は fs_dir_path が basePath を脱出するライセンス行を
// 1 行投入する (DB 汚染の再現)。
func seedEscapingLicense(t *testing.T, q *repository.Queries, fsDirPath string) repository.License {
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
	lic, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID:          p.ID,
		OwningDepartmentID: d.ID,
		LicenseSlug:        "2024-jouki",
		DisplayName:        "Acrobat 年間契約",
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          fsDirPath,
	})
	if err != nil {
		t.Fatalf("CreateLicense: %v", err)
	}
	return lic
}

// TestRegenerate_RejectsBaseEscape は fs_dir_path が汚染されたライセンスの
// Regenerate がエラーになり、basePath の外に何も書き込まれないことを
// 確認する。
func TestRegenerate_RejectsBaseEscape(t *testing.T) {
	t.Parallel()
	base, parent := newBase(t)
	sqlDB := handlertest.NewTestDB(t)
	q := repository.New(sqlDB)
	lic := seedEscapingLicense(t, q, "../evil")

	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := licensefs.Regenerate(context.Background(), q, base, lic.ID, now); err == nil {
		t.Fatal("Regenerate with escaping fs_dir_path should return an error")
	}

	// base の外 (親ディレクトリ) には base 以外の何も作られていない。
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "base" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("nothing must be written outside base, parent contains %v", names)
	}
}
