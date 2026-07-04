// Package filestore はライセンス証書ファイルの保存・オープンを担う。
// 仕様 §8.3 のアップロード検証 (サイズ上限・許可拡張子・マジックバイト
// 実判定・許可 MIME 照合) を保存時に行い、Content-Type ヘッダは信用しない。
// パスは base (file_store.base_path) からの相対 / 区切りで受け取り、
// Windows でも動くよう filepath.FromSlash で変換して扱う。
package filestore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/slug"
)

// Store はファイルストアへの保存・オープンを提供する。
type Store struct {
	base     string
	maxBytes int64
	allowed  map[string]bool
}

// SavedFile は Save の結果。Name は実際に保存されたファイル名
// (Slugify 済み + 衝突サフィックス付きの可能性あり)。
type SavedFile struct {
	Name   string
	SHA256 string
	Size   int64
	MIME   string
}

// extToMIME は許可拡張子 (仕様 §8.3) と、その拡張子で期待する MIME の
// 対応。拡張子がこの表に無ければ許可外として拒否する。
var extToMIME = map[string]string{
	".pdf":  "application/pdf",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
}

// New は cfg から Store を作る。cfg は config.Load の validate 済み
// (UploadMaxBytes / AllowedMimeTypes は既定値解決済み) を想定する。
func New(cfg config.FileStoreConfig) *Store {
	allowed := make(map[string]bool, len(cfg.AllowedMimeTypes))
	for _, m := range cfg.AllowedMimeTypes {
		allowed[m] = true
	}
	return &Store{
		base:     cfg.BasePath,
		maxBytes: cfg.UploadMaxBytes,
		allowed:  allowed,
	}
}

// Save は r の内容を検証して dir (base からの相対、/ 区切り) に保存し、
// 保存名・sha256・サイズ・判定 MIME を返す。検証順は仕様 §8.3 のとおり
// サイズ上限 → 拡張子 → マジックバイト → 許可 MIME。保存名は originalName
// の拡張子を保持しつつ本体を slug.Slugify で正規化し、同名衝突時は
// _2, _3... サフィックスを付ける。
func (s *Store) Save(dir, originalName string, r io.Reader) (SavedFile, error) {
	// パス区切りが混入していても最終要素だけ使う (パストラバーサル防止)。
	base := filepath.Base(filepath.FromSlash(originalName))
	ext := strings.ToLower(filepath.Ext(base))
	wantMIME, ok := extToMIME[ext]
	if !ok {
		return SavedFile{}, fmt.Errorf("file extension %q is not allowed (want .pdf/.png/.jpg/.jpeg)", ext)
	}

	// maxBytes+1 まで読み、超過を検出する。20 MiB 上限なのでメモリ保持で
	// 十分 (Plan の想定リスク参照。巨大化したらストリーミングに変える)。
	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return SavedFile{}, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return SavedFile{}, fmt.Errorf("upload exceeds size limit %d bytes", s.maxBytes)
	}

	mime := SniffMIME(data)
	if mime != wantMIME {
		return SavedFile{}, fmt.Errorf("file content does not match %q (magic bytes indicate %q)", wantMIME, orUnknown(mime))
	}
	if !s.allowed[mime] {
		return SavedFile{}, fmt.Errorf("mime type %q is not in allowed_mime_types", mime)
	}

	dirAbs := filepath.Join(s.base, filepath.FromSlash(dir))
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return SavedFile{}, fmt.Errorf("create directory %s: %w", dirAbs, err)
	}

	stem := slug.Slugify(strings.TrimSuffix(base, filepath.Ext(base)))
	name, err := writeUnique(dirAbs, stem, ext, data)
	if err != nil {
		return SavedFile{}, err
	}

	sum := sha256.Sum256(data)
	return SavedFile{
		Name:   name,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(data)),
		MIME:   mime,
	}, nil
}

// writeUnique は dirAbs に stem+ext で排他作成し、既存なら stem_2, stem_3...
// と衝突しない名前を探して書く (仕様 §3.2 のサフィックス規則)。
// O_EXCL で作成するため並行実行でも同名を上書きしない。
func writeUnique(dirAbs, stem, ext string, data []byte) (string, error) {
	for i := 1; ; i++ {
		name := stem + ext
		if i > 1 {
			name = fmt.Sprintf("%s_%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dirAbs, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create file %s: %w", name, err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return "", fmt.Errorf("write file %s: %w", name, err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(f.Name())
			return "", fmt.Errorf("close file %s: %w", name, err)
		}
		return name, nil
	}
}

// Open はダウンロード用に相対 stored_path (base からの相対、/ 区切り) を
// 開く。stored_path は DB 由来だが多層防御として、絶対パス・base 配下から
// 脱出するパスを拒否する。
func (s *Store) Open(storedPath string) (*os.File, error) {
	p := filepath.FromSlash(storedPath)
	if filepath.IsAbs(p) {
		return nil, fmt.Errorf("stored path must be relative to base, got absolute path %q", storedPath)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("stored path %q escapes the file store base", storedPath)
	}
	f, err := os.Open(filepath.Join(s.base, clean))
	if err != nil {
		return nil, fmt.Errorf("open stored file %q: %w", storedPath, err)
	}
	return f, nil
}

// マジックバイト (仕様 §8.3): %PDF- / PNG シグネチャ / FF D8 FF。
var (
	magicPDF  = []byte("%PDF-")
	magicPNG  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	magicJPEG = []byte{0xFF, 0xD8, 0xFF}
)

// SniffMIME は先頭バイトのマジックで application/pdf / image/png /
// image/jpeg を判定する。どれにも一致しなければ空文字列を返す。
func SniffMIME(data []byte) string {
	switch {
	case bytes.HasPrefix(data, magicPDF):
		return "application/pdf"
	case bytes.HasPrefix(data, magicPNG):
		return "image/png"
	case bytes.HasPrefix(data, magicJPEG):
		return "image/jpeg"
	default:
		return ""
	}
}

// orUnknown はエラーメッセージ用に空の MIME を "unknown" に置き換える。
func orUnknown(mime string) string {
	if mime == "" {
		return "unknown"
	}
	return mime
}
