package filestore_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/filestore"
)

// 各 MIME のマジックバイトを持つ最小テストデータ (仕様 §8.3:
// %PDF- / PNG シグネチャ / FF D8 FF)。
var (
	pdfData  = []byte("%PDF-1.4\n1 0 obj\n<< >>\nendobj\n%%EOF\n")
	pngData  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("fake png body")...)
	jpegData = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("fake jpeg body")...)
)

func newStore(t *testing.T, maxBytes int64, allowed []string) (*filestore.Store, string) {
	t.Helper()
	base := t.TempDir()
	cfg := config.FileStoreConfig{
		BasePath:         base,
		UploadMaxBytes:   maxBytes,
		AllowedMimeTypes: allowed,
	}
	if len(allowed) == 0 {
		cfg.AllowedMimeTypes = []string{"application/pdf", "image/png", "image/jpeg"}
	}
	return filestore.New(cfg), base
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestSave_validFiles は PDF / PNG / JPEG の正常保存を検証する。
// 保存名は slug.Slugify 済み (拡張子は保持)、sha256・サイズ・判定 MIME が
// 返り、物理ファイルが base/dir 配下に同一バイト列で置かれること。
func TestSave_validFiles(t *testing.T) {
	tests := []struct {
		name     string
		original string
		data     []byte
		wantName string
		wantMIME string
	}{
		{
			name:     "pdf with space in name",
			original: "証書 2024.pdf",
			data:     pdfData,
			wantName: "証書_2024.pdf",
			wantMIME: "application/pdf",
		},
		{
			name:     "png",
			original: "screenshot.png",
			data:     pngData,
			wantName: "screenshot.png",
			wantMIME: "image/png",
		},
		{
			name:     "jpeg with .jpg extension",
			original: "photo.jpg",
			data:     jpegData,
			wantName: "photo.jpg",
			wantMIME: "image/jpeg",
		},
		{
			name:     "jpeg with .jpeg extension",
			original: "photo2.jpeg",
			data:     jpegData,
			wantName: "photo2.jpeg",
			wantMIME: "image/jpeg",
		},
		{
			name:     "forbidden characters slugified",
			original: `注文書:2024?.pdf`,
			data:     pdfData,
			wantName: "注文書_2024_.pdf",
			wantMIME: "application/pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, base := newStore(t, 1<<20, nil)
			dir := "licenses/Adobe/Acrobat/契約_2024"

			got, err := s.Save(dir, tt.original, bytes.NewReader(tt.data))
			if err != nil {
				t.Fatalf("Save() unexpected error: %v", err)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.SHA256 != sha256Hex(tt.data) {
				t.Errorf("SHA256 = %q, want %q", got.SHA256, sha256Hex(tt.data))
			}
			if got.Size != int64(len(tt.data)) {
				t.Errorf("Size = %d, want %d", got.Size, len(tt.data))
			}
			if got.MIME != tt.wantMIME {
				t.Errorf("MIME = %q, want %q", got.MIME, tt.wantMIME)
			}

			onDisk, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(dir), tt.wantName))
			if err != nil {
				t.Fatalf("read stored file: %v", err)
			}
			if !bytes.Equal(onDisk, tt.data) {
				t.Error("stored file bytes differ from input")
			}
		})
	}
}

// TestSave_magicMismatch は拡張子偽装 (中身が txt の .pdf) を拒否する。
func TestSave_magicMismatch(t *testing.T) {
	s, base := newStore(t, 1<<20, nil)

	_, err := s.Save("licenses/v/p/l", "偽装.pdf", bytes.NewReader([]byte("this is plain text, not a pdf")))
	if err == nil {
		t.Fatal("Save() expected error for txt content in .pdf, got nil")
	}
	assertNoFiles(t, base)
}

// TestSave_disallowedExtension は許可外拡張子 (.exe) を拒否する。
// 中身が正しい PDF でも拡張子で落とす (仕様 §8.3 の許可拡張子リスト照合)。
func TestSave_disallowedExtension(t *testing.T) {
	s, base := newStore(t, 1<<20, nil)

	_, err := s.Save("licenses/v/p/l", "installer.exe", bytes.NewReader(pdfData))
	if err == nil {
		t.Fatal("Save() expected error for .exe extension, got nil")
	}
	assertNoFiles(t, base)
}

// TestSave_maxBytesExceeded はサイズ上限超過を拒否する。上限ちょうどは
// 通す (境界)。
func TestSave_maxBytesExceeded(t *testing.T) {
	s, base := newStore(t, int64(len(pdfData)), nil)

	if _, err := s.Save("licenses/v/p/l", "just.pdf", bytes.NewReader(pdfData)); err != nil {
		t.Fatalf("Save() at exactly maxBytes should succeed, got error: %v", err)
	}

	over := append(append([]byte{}, pdfData...), 'x')
	_, err := s.Save("licenses/v/p/l", "over.pdf", bytes.NewReader(over))
	if err == nil {
		t.Fatal("Save() expected error for data exceeding maxBytes, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(base, "licenses", "v", "p", "l", "over.pdf")); !os.IsNotExist(statErr) {
		t.Errorf("oversized file should not remain on disk, stat err = %v", statErr)
	}
}

// TestSave_mimeNotAllowed は判定 MIME が allowed_mime_types に無い場合を
// 拒否する (PDF のみ許可の Store に PNG を保存)。
func TestSave_mimeNotAllowed(t *testing.T) {
	s, base := newStore(t, 1<<20, []string{"application/pdf"})

	_, err := s.Save("licenses/v/p/l", "image.png", bytes.NewReader(pngData))
	if err == nil {
		t.Fatal("Save() expected error for MIME not in allowed list, got nil")
	}
	assertNoFiles(t, base)
}

// TestSave_collisionSuffix は同名保存の衝突回避を検証する。2 個目は
// name_2.pdf、3 個目は name_3.pdf (仕様 §3.2 のサフィックス規則)。
func TestSave_collisionSuffix(t *testing.T) {
	s, _ := newStore(t, 1<<20, nil)
	dir := "licenses/v/p/l"

	first, err := s.Save(dir, "契約書.pdf", bytes.NewReader(pdfData))
	if err != nil {
		t.Fatalf("Save() #1 unexpected error: %v", err)
	}
	if first.Name != "契約書.pdf" {
		t.Errorf("first Name = %q, want %q", first.Name, "契約書.pdf")
	}

	second, err := s.Save(dir, "契約書.pdf", bytes.NewReader(pdfData))
	if err != nil {
		t.Fatalf("Save() #2 unexpected error: %v", err)
	}
	if second.Name != "契約書_2.pdf" {
		t.Errorf("second Name = %q, want %q", second.Name, "契約書_2.pdf")
	}

	third, err := s.Save(dir, "契約書.pdf", bytes.NewReader(pdfData))
	if err != nil {
		t.Fatalf("Save() #3 unexpected error: %v", err)
	}
	if third.Name != "契約書_3.pdf" {
		t.Errorf("third Name = %q, want %q", third.Name, "契約書_3.pdf")
	}
}

// TestOpen_ok は保存済みファイルを相対 stored_path で開けることを検証する。
func TestOpen_ok(t *testing.T) {
	s, _ := newStore(t, 1<<20, nil)
	dir := "licenses/v/p/l"

	saved, err := s.Save(dir, "証書.pdf", bytes.NewReader(pdfData))
	if err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	f, err := s.Open(dir + "/" + saved.Name)
	if err != nil {
		t.Fatalf("Open() unexpected error: %v", err)
	}
	defer func() { _ = f.Close() }()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	if !bytes.Equal(got, pdfData) {
		t.Error("Open() returned different bytes than saved")
	}
}

// TestOpen_pathTraversal は ../ を含む stored_path を拒否する
// (stored_path は DB 由来だが多層防御)。
func TestOpen_pathTraversal(t *testing.T) {
	s, base := newStore(t, 1<<20, nil)

	// base の外に実在するファイルを用意し、実在してもなお拒否されることを
	// 確認する (存在チェックより前に弾く)。
	outside := filepath.Join(filepath.Dir(base), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	for _, p := range []string{
		"../outside.txt",
		"licenses/../../outside.txt",
		"..",
	} {
		if f, err := s.Open(p); err == nil {
			_ = f.Close()
			t.Errorf("Open(%q) expected error for path escaping base, got nil", p)
		}
	}
}

// TestOpen_absolutePath は base 外の絶対パスを拒否する。
func TestOpen_absolutePath(t *testing.T) {
	s, base := newStore(t, 1<<20, nil)

	outside := filepath.Join(filepath.Dir(base), "abs-outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if f, err := s.Open(outside); err == nil {
		_ = f.Close()
		t.Errorf("Open(%q) expected error for absolute path outside base, got nil", outside)
	}
}

// assertNoFiles は base 配下に通常ファイルが 1 つも残っていないことを
// 検証する (検証エラー時に部分書込みが残らないこと)。
func assertNoFiles(t *testing.T, base string) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Errorf("unexpected file left on disk: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk base dir: %v", err)
	}
}
