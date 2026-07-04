package metayml_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/metayml"
)

func strPtr(s string) *string { return &s }
func i64Ptr(i int64) *int64   { return &i }
func timePtr(t time.Time) *time.Time {
	return &t
}

// TestWrite_specExample は仕様 §5.2 の meta.yml 例と同キー順・同形式で
// 出力されることを検証する。ヘッダコメント 2 行、空値は「キー: のみ」、
// 日付は YYYY-MM-DD、日時は JST ISO8601 オフセット表記 (§8.6。入力は
// UTC で渡し、JST への変換込みで確認する)。
func TestWrite_specExample(t *testing.T) {
	m := metayml.Meta{
		Product:          "Adobe Acrobat Pro DC",
		Vendor:           "Adobe",
		Edition:          nil,
		LicenseSlug:      "契約_2024-04_営業部",
		DisplayName:      "営業部 Acrobat Pro DC 契約",
		TotalCount:       i64Ptr(10),
		CountUnit:        "device",
		ContractType:     "perpetual",
		PurchasedAt:      timePtr(time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)),
		StartedAt:        timePtr(time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)),
		ExpiresAt:        nil,
		OwningDepartment: "営業部",
		VendorOrderNo:    strPtr("PO-2024-0123"),
		Purchaser:        strPtr("○○商事"),
		UnitPrice:        i64Ptr(60000),
		Currency:         strPtr("JPY"),
		Documents: []metayml.Document{
			{
				Filename: "証書_2024.pdf",
				SHA256:   "a3f5...",
				// UTC 01:23 → JST 10:23 (+09:00)
				UploadedAt: time.Date(2024, 4, 15, 1, 23, 0, 0, time.UTC),
			},
		},
		Note: strPtr("ボリュームライセンス\n"),
		// UTC 11/30 18:00 → JST 12/1 03:00 (日付も繰り上がる)
		LastUpdatedByApp: time.Date(2025, 11, 30, 18, 0, 0, 0, time.UTC),
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "meta.yml")
	if err := metayml.Write(path, m); err != nil {
		t.Fatalf("Write() unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta.yml: %v", err)
	}

	want := `# このファイルは本システムが自動生成しています
# 手動編集は次回同期時に上書きされます
product: Adobe Acrobat Pro DC
vendor: Adobe
edition:
license_slug: 契約_2024-04_営業部
display_name: 営業部 Acrobat Pro DC 契約
total_count: 10
count_unit: device
contract_type: perpetual
purchased_at: 2024-04-01
started_at: 2024-04-01
expires_at:
owning_department: 営業部
vendor_order_no: PO-2024-0123
purchaser: ○○商事
unit_price: 60000
currency: JPY
documents:
  - filename: 証書_2024.pdf
    sha256: a3f5...
    uploaded_at: 2024-04-15T10:23:00+09:00
note: |
  ボリュームライセンス
last_updated_by_app: 2025-12-01T03:00:00+09:00
`
	if string(got) != want {
		t.Errorf("meta.yml mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWrite_minimal は任意項目が未設定でも全キーが出力されること
// (空値は「キー: のみ」、documents は []) を検証する。
func TestWrite_minimal(t *testing.T) {
	m := metayml.Meta{
		Product:          "7-Zip",
		Vendor:           "Igor Pavlov",
		LicenseSlug:      "サイトライセンス",
		DisplayName:      "全社 7-Zip",
		CountUnit:        "device",
		ContractType:     "perpetual",
		OwningDepartment: "情報システム部",
		LastUpdatedByApp: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC),
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "meta.yml")
	if err := metayml.Write(path, m); err != nil {
		t.Fatalf("Write() unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta.yml: %v", err)
	}

	want := `# このファイルは本システムが自動生成しています
# 手動編集は次回同期時に上書きされます
product: 7-Zip
vendor: Igor Pavlov
edition:
license_slug: サイトライセンス
display_name: 全社 7-Zip
total_count:
count_unit: device
contract_type: perpetual
purchased_at:
started_at:
expires_at:
owning_department: 情報システム部
vendor_order_no:
purchaser:
unit_price:
currency:
documents: []
note:
last_updated_by_app: 2026-01-01T12:00:00+09:00
`
	if string(got) != want {
		t.Errorf("meta.yml mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWrite_tmpRename は tmp + rename 書込みを検証する: 書込み後に
// .tmp 等の一時ファイルが残らず、既存 meta.yml の上書きもできること。
func TestWrite_tmpRename(t *testing.T) {
	m := metayml.Meta{
		Product:          "7-Zip",
		Vendor:           "Igor Pavlov",
		LicenseSlug:      "サイトライセンス",
		DisplayName:      "全社 7-Zip",
		CountUnit:        "device",
		ContractType:     "perpetual",
		OwningDepartment: "情報システム部",
		LastUpdatedByApp: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC),
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "meta.yml")

	if err := metayml.Write(path, m); err != nil {
		t.Fatalf("Write() #1 unexpected error: %v", err)
	}

	// 上書き (ライセンス更新時の再生成)
	m.DisplayName = "全社 7-Zip (更新)"
	if err := metayml.Write(path, m); err != nil {
		t.Fatalf("Write() #2 unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "meta.yml" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir entries = %v, want exactly [meta.yml] (no leftover tmp files)", names)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta.yml: %v", err)
	}
	if want := "\ndisplay_name: 全社 7-Zip (更新)\n"; !strings.Contains(string(got), want) {
		t.Errorf("meta.yml after overwrite does not contain %q\n--- got ---\n%s", want, got)
	}
}
