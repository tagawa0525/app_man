// export_test は /admin/export (仕様 §5.10 / Plan admin-export.md) の
// エクスポート生成本体の単体テスト。
//
//   - WriteExcel: 10 シート構成 (業務データの正本のみ、sessions /
//     audit_logs は除外)・1 行目ヘッダ・全行エクスポート (論理削除済み
//     行も含む)・product_keys 列の opt-in
//   - WriteZip: VACUUM INTO による db-snapshot.db (開いて SELECT 可能な
//     完成品) + licenses/ ツリー全量
package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/tagawa0525/app_man/internal/export"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// wantSheets は WriteExcel が生成するシート名の期待値 (Plan の 10 シート)。
var wantSheets = []string{
	"vendors",
	"products",
	"departments",
	"users",
	"devices",
	"licenses",
	"user_assignments",
	"device_assignments",
	"approvals",
	"app_settings",
}

const testProductKey = "KEY-AAA-111"

func ptr[T any](v T) *T { return &v }

// seedAllTables は 10 シートすべてにデータ行が出るよう各テーブルへ 1 行
// 以上を投入する。user_assignments は解約済み行も足し、「全データ =
// 論理削除済みも含む」ことを検証できるようにする。
func seedAllTables(t *testing.T, q *repository.Queries) {
	t.Helper()
	ctx := context.Background()

	vendor, err := q.CreateVendor(ctx, repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}
	product, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID:              vendor.ID,
		CanonicalName:         "Acrobat Pro",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("seed CreateProduct: %v", err)
	}
	dept, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{
		Code: "D001",
		Name: "情報システム部",
	})
	if err != nil {
		t.Fatalf("seed CreateDepartment: %v", err)
	}
	user, err := q.CreateUser(ctx, repository.CreateUserParams{
		EmployeeCode: "E0001",
		Name:         "山田太郎",
		DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("seed CreateUser: %v", err)
	}
	device, err := q.CreateDevice(ctx, repository.CreateDeviceParams{
		AssetCode:    "PC-0001",
		Hostname:     ptr("host0001"),
		DepartmentID: &dept.ID,
	})
	if err != nil {
		t.Fatalf("seed CreateDevice: %v", err)
	}
	license, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID:          product.ID,
		OwningDepartmentID: dept.ID,
		LicenseSlug:        "2026-genki",
		DisplayName:        "Acrobat Pro 2026",
		TotalCount:         ptr(int64(10)),
		CountUnit:          "user",
		ContractType:       "perpetual",
		ProductKeys:        ptr(testProductKey),
		FsDirPath:          "licenses/adobe/acrobat-pro/2026-genki",
	})
	if err != nil {
		t.Fatalf("seed CreateLicense: %v", err)
	}
	active, err := q.CreateUserAssignment(ctx, repository.CreateUserAssignmentParams{
		LicenseID: license.ID,
		UserID:    user.ID,
	})
	if err != nil {
		t.Fatalf("seed CreateUserAssignment (active): %v", err)
	}
	// 解約済み行: いったん作って revoke する。全件エクスポートなので
	// revoked_at 付きの行もシートに出る。
	if _, err := q.RevokeUserAssignment(ctx, repository.RevokeUserAssignmentParams{
		ID:        active.ID,
		LicenseID: license.ID,
	}); err != nil {
		t.Fatalf("seed RevokeUserAssignment: %v", err)
	}
	if _, err := q.CreateUserAssignment(ctx, repository.CreateUserAssignmentParams{
		LicenseID: license.ID,
		UserID:    user.ID,
	}); err != nil {
		t.Fatalf("seed CreateUserAssignment (2nd): %v", err)
	}
	if _, err := q.CreateDeviceAssignment(ctx, repository.CreateDeviceAssignmentParams{
		LicenseID: license.ID,
		DeviceID:  device.ID,
	}); err != nil {
		t.Fatalf("seed CreateDeviceAssignment: %v", err)
	}
	if _, err := q.CreateApproval(ctx, repository.CreateApprovalParams{
		DepartmentID: dept.ID,
		ProductID:    product.ID,
		Status:       "approved",
		ScopeType:    "department_wide",
	}); err != nil {
		t.Fatalf("seed CreateApproval: %v", err)
	}
	if _, err := q.UpsertAppSetting(ctx, repository.UpsertAppSettingParams{
		Key:   "retention_days_audit_logs",
		Value: ptr("30"),
	}); err != nil {
		t.Fatalf("seed UpsertAppSetting: %v", err)
	}
}

// writeExcelToFile は WriteExcel の出力を excelize で読み戻す。
func writeExcelToFile(t *testing.T, db *sql.DB, includeKeys bool) *excelize.File {
	t.Helper()
	var buf bytes.Buffer
	if err := export.WriteExcel(context.Background(), repository.New(db), &buf, includeKeys); err != nil {
		t.Fatalf("WriteExcel(includeKeys=%v): %v", includeKeys, err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("excelize.OpenReader: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// sheetRows は 1 シートの全行を返す。
func sheetRows(t *testing.T, f *excelize.File, sheet string) [][]string {
	t.Helper()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("GetRows(%s): %v", sheet, err)
	}
	return rows
}

// --- WriteExcel ---------------------------------------------------------

// 10 シートが Plan の順で並び、各シートに 1 行目ヘッダ + データ行が入る。
func TestWriteExcel_SheetsAndRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAllTables(t, repository.New(db))

	f := writeExcelToFile(t, db, false)

	if got := f.GetSheetList(); !slices.Equal(got, wantSheets) {
		t.Fatalf("sheet list = %v, want %v", got, wantSheets)
	}

	// 各シートの 1 行目はヘッダ (先頭列は id ないし key)、2 行目以降に
	// データ行がある。
	for _, sheet := range wantSheets {
		rows := sheetRows(t, f, sheet)
		if len(rows) < 2 {
			t.Errorf("sheet %s has %d rows, want header + at least 1 data row", sheet, len(rows))
			continue
		}
		wantFirst := "id"
		if sheet == "app_settings" {
			wantFirst = "key"
		}
		if rows[0][0] != wantFirst {
			t.Errorf("sheet %s header[0] = %q, want %q", sheet, rows[0][0], wantFirst)
		}
	}

	// 代表値の存在確認。
	vendors := sheetRows(t, f, "vendors")
	if len(vendors) != 2 || !slices.Contains(vendors[1], "Adobe") {
		t.Errorf("vendors rows = %v, want 1 data row containing Adobe", vendors)
	}
	// user_assignments は解約済み + 有効の 2 データ行 (全件エクスポート)。
	ua := sheetRows(t, f, "user_assignments")
	if len(ua) != 3 {
		t.Errorf("user_assignments rows = %d (incl. header), want 3 (revoked も含む)", len(ua))
	}
}

// デフォルト (includeKeys=false) では licenses シートに product_keys 列
// 自体が存在せず、キー文字列がブックのどこにも現れない。
func TestWriteExcel_ExcludesKeysByDefault(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAllTables(t, repository.New(db))

	f := writeExcelToFile(t, db, false)

	rows := sheetRows(t, f, "licenses")
	if len(rows) < 2 {
		t.Fatalf("licenses rows = %d, want header + data", len(rows))
	}
	if slices.Contains(rows[0], "product_keys") {
		t.Errorf("licenses header %v contains product_keys, want column absent", rows[0])
	}
	for _, sheet := range wantSheets {
		for i, row := range sheetRows(t, f, sheet) {
			if slices.Contains(row, testProductKey) {
				t.Errorf("sheet %s row %d leaks product key %q", sheet, i+1, testProductKey)
			}
		}
	}
}

// includeKeys=true では product_keys 列がヘッダに現れ、キーの平文が入る。
func TestWriteExcel_IncludesKeysWhenOptedIn(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAllTables(t, repository.New(db))

	f := writeExcelToFile(t, db, true)

	rows := sheetRows(t, f, "licenses")
	if len(rows) < 2 {
		t.Fatalf("licenses rows = %d, want header + data", len(rows))
	}
	col := slices.Index(rows[0], "product_keys")
	if col < 0 {
		t.Fatalf("licenses header %v does not contain product_keys", rows[0])
	}
	if len(rows[1]) <= col || rows[1][col] != testProductKey {
		t.Errorf("licenses data row %v: product_keys column = missing or wrong, want %q", rows[1], testProductKey)
	}
}

// --- WriteZip -----------------------------------------------------------

// zipEntries は ZIP を読み戻して name → 内容のマップを返す。
func zipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	entries := make(map[string][]byte, len(zr.File))
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", zf.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", zf.Name, err)
		}
		entries[zf.Name] = b
	}
	return entries
}

// db-snapshot.db (VACUUM INTO の完成品) と licenses/ ツリーが入る。
// スナップショットは展開して sql.Open → SELECT できる。
func TestWriteZip_SnapshotAndLicenseTree(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	if _, err := q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "Adobe"}); err != nil {
		t.Fatalf("seed CreateVendor: %v", err)
	}

	// FS 側: <base>/licenses/ 配下に証書と meta.yml を配置する。
	base := t.TempDir()
	licDir := filepath.Join(base, "licenses", "adobe", "acrobat-pro", "2026-genki")
	if err := os.MkdirAll(licDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, content := range map[string]string{
		"meta.yml":        "schema_version: 1\n",
		"certificate.pdf": "%PDF-1.4 dummy",
	} {
		if err := os.WriteFile(filepath.Join(licDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	var buf bytes.Buffer
	if err := export.WriteZip(context.Background(), db, base, &buf); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	entries := zipEntries(t, buf.Bytes())

	snapshot, ok := entries["db-snapshot.db"]
	if !ok {
		t.Fatalf("zip entries %v do not contain db-snapshot.db", keysOf(entries))
	}
	for _, want := range []string{
		"licenses/adobe/acrobat-pro/2026-genki/meta.yml",
		"licenses/adobe/acrobat-pro/2026-genki/certificate.pdf",
	} {
		if _, ok := entries[want]; !ok {
			t.Errorf("zip entries %v do not contain %s", keysOf(entries), want)
		}
	}

	// スナップショットを一時ファイルへ展開して開き、シードした行が
	// SELECT できること (= 完成品の SQLite ファイルであること) を確認。
	snapPath := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(snapPath, snapshot, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	snapDB, err := sql.Open("sqlite", snapPath)
	if err != nil {
		t.Fatalf("sql.Open(snapshot): %v", err)
	}
	defer func() { _ = snapDB.Close() }()
	var n int
	if err := snapDB.QueryRow("SELECT count(*) FROM vendors").Scan(&n); err != nil {
		t.Fatalf("SELECT from snapshot: %v", err)
	}
	if n != 1 {
		t.Errorf("snapshot vendors count = %d, want 1", n)
	}
}

// licenses/ ディレクトリが無い環境でもエラーにせず db-snapshot.db のみの
// ZIP を返す (FS が空でもエクスポートはブロックしない)。
func TestWriteZip_NoLicensesDir(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	var buf bytes.Buffer
	if err := export.WriteZip(context.Background(), db, t.TempDir(), &buf); err != nil {
		t.Fatalf("WriteZip without licenses dir: %v", err)
	}
	entries := zipEntries(t, buf.Bytes())
	if len(entries) != 1 {
		t.Errorf("zip entries = %v, want only db-snapshot.db", keysOf(entries))
	}
	if _, ok := entries["db-snapshot.db"]; !ok {
		t.Errorf("zip entries %v do not contain db-snapshot.db", keysOf(entries))
	}
}

// keysOf はエラーメッセージ用にエントリ名一覧を返す。
func keysOf(m map[string][]byte) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}
