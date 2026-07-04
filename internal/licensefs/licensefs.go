// Package licensefs はライセンス契約フォルダの物理配置 (仕様 §3.2 / §5.2)
// を操作する。meta.yml の再生成は web 層の 3 トリガ (ライセンス作成・更新・
// 証書アップロード) と appmgr-generate-meta の一括再生成で共有するため、
// web 層から本パッケージへ抽出した (重複 3 回超の抽象化基準)。
package licensefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/metayml"
	"github.com/tagawa0525/app_man/internal/repository"
)

// DirAbs は fs_dir_path (/ 区切り相対) を basePath 配下の絶対パスにする。
// fs_dir_path は DB 由来 (アプリが自前生成) だが、汚染で basePath の外を
// 指す値 (絶対パス・Clean 後に .. で脱出・空) は拒否する
// (filestore.Store.Open と同じ多層防御)。
func DirAbs(basePath, fsDirPath string) (string, error) {
	if fsDirPath == "" {
		return "", errors.New("fs_dir_path must not be empty")
	}
	p := filepath.FromSlash(fsDirPath)
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("fs_dir_path must be relative to base, got absolute path %q", fsDirPath)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("fs_dir_path %q escapes the file store base", fsDirPath)
	}
	return filepath.Join(basePath, clean), nil
}

// MetaExists は fsDirPath 配下に meta.yml が通常ファイルとして存在するかを
// 返す (generate-meta の dry-run が would_create を数えるための判定)。
// fsDirPath が basePath を脱出する場合は false ではなく error を返す
// (汚染行が「meta 無し = would_create」として黙って集計されるのを防ぎ、
// 呼び出し側が failed としてログできる)。
func MetaExists(basePath, fsDirPath string) (bool, error) {
	dirAbs, err := DirAbs(basePath, fsDirPath)
	if err != nil {
		return false, err
	}
	metaPath := filepath.Join(dirAbs, "meta.yml")
	fi, err := os.Stat(metaPath)
	switch {
	case err == nil && fi.Mode().IsRegular():
		return true, nil
	case err == nil:
		// ディレクトリ等が meta.yml の名前を占有している。「存在しない =
		// would_create」と誤答すると実行時に必ず失敗する行を予告できない
		return false, fmt.Errorf("meta path %s exists but is not a regular file", metaPath)
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		// EACCES 等。存在不明を「無し」に倒さずエラーとして可視化する
		return false, fmt.Errorf("stat %s: %w", metaPath, err)
	}
}

// Regenerate は物理ディレクトリを確保して meta.yml を現在の DB 内容で
// 書き直す (仕様 §5.2 / §8.6)。now は last_updated_by_app に使う
// (web は time.Now() を渡し、CLI テストは固定時刻を注入する)。
// 呼び出し側でエラーをログしてブロックしない (FS/DB のズレは警告のみの思想)。
func Regenerate(ctx context.Context, q *repository.Queries, basePath string, licenseID int64, now time.Time) error {
	row, err := q.GetLicenseByID(ctx, licenseID)
	if err != nil {
		return fmt.Errorf("get license for meta: %w", err)
	}
	prod, err := q.GetProduct(ctx, row.ProductID)
	if err != nil {
		return fmt.Errorf("get product for meta: %w", err)
	}
	docs, err := q.ListLicenseDocumentsByLicense(ctx, licenseID)
	if err != nil {
		return fmt.Errorf("list documents for meta: %w", err)
	}

	dirAbs, err := DirAbs(basePath, row.FsDirPath)
	if err != nil {
		return fmt.Errorf("resolve license dir: %w", err)
	}
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return fmt.Errorf("create license dir %s: %w", dirAbs, err)
	}

	m := metayml.Meta{
		Product:          row.ProductName,
		Vendor:           row.VendorName,
		Edition:          prod.Edition,
		LicenseSlug:      row.LicenseSlug,
		DisplayName:      row.DisplayName,
		TotalCount:       row.TotalCount,
		CountUnit:        row.CountUnit,
		ContractType:     row.ContractType,
		PurchasedAt:      row.PurchasedAt,
		StartedAt:        row.StartedAt,
		ExpiresAt:        row.ExpiresAt,
		OwningDepartment: row.DepartmentName,
		VendorOrderNo:    row.VendorOrderNo,
		Purchaser:        row.Purchaser,
		UnitPrice:        row.UnitPrice,
		Currency:         row.Currency,
		Note:             row.Note,
		LastUpdatedByApp: now,
	}
	for _, d := range docs {
		m.Documents = append(m.Documents, metayml.Document{
			// meta.yml はファイルサーバを直接覗く人向けなので、フォルダ内に
			// 実在する保存名を載せる (original_filename は DB が保持)。
			Filename:   path.Base(d.StoredPath),
			SHA256:     d.Sha256,
			UploadedAt: d.UploadedAt,
		})
	}
	return metayml.Write(filepath.Join(dirAbs, "meta.yml"), m)
}
