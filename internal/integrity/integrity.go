// Package integrity は FS (ライセンス契約フォルダ) と DB の整合性チェック
// (仕様 §5.12) を提供する。appmgr-check-integrity CLI と /admin/integrity
// 画面 (別 PR) が同じ Scan を共有するため、検出ロジックをここに置く。
// 所見は Kind 定数 + 生データに留め、文言の日本語化は表示側の責務。
package integrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// 所見の種類 (仕様 §5.12 の 5 パターン + 汚染パス + meta 生成失敗)。
const (
	// KindFileMissing: license_documents.stored_path の実体が FS に無い。
	KindFileMissing = "file_missing"
	// KindUnregisteredFile: 契約フォルダ内にあるが DB に登録の無いファイル
	// (meta.yml は除外。「人が直接置く」運用があるため正常系でも起こる)。
	KindUnregisteredFile = "unregistered_file"
	// KindSha256Mismatch: ファイル内容の sha256 が DB 記録と一致しない。
	KindSha256Mismatch = "sha256_mismatch"
	// KindDirMissing: fs_dir_path の物理ディレクトリが無い。
	KindDirMissing = "dir_missing"
	// KindOrphanDir: licenses/ 配下の末端ディレクトリがどのライセンスの
	// fs_dir_path にも対応しない。
	KindOrphanDir = "orphan_dir"
	// KindInvalidPath: fs_dir_path / stored_path が basePath を脱出する等、
	// パスとして不正 (汚染行)。
	KindInvalidPath = "invalid_path"
	// KindMetaGenerateFailed: meta.yml の自動生成に失敗した。
	KindMetaGenerateFailed = "meta_generate_failed"
)

// Kinds は全 Kind の一覧 (表示・サマリ集計用の固定順)。
var Kinds = []string{
	KindFileMissing,
	KindSha256Mismatch,
	KindUnregisteredFile,
	KindDirMissing,
	KindOrphanDir,
	KindInvalidPath,
	KindMetaGenerateFailed,
}

// Finding は 1 件の所見。Path は原則 basePath 相対 (/ 区切り) だが、
// invalid_path では DB に入っていた不正値 (../ や絶対パス) をそのまま
// 保持する (原因調査のため加工しない)。orphan_dir の
// ように対応ライセンスが無い所見は LicenseID = 0。
type Finding struct {
	Kind      string
	LicenseID int64
	Path      string
	Detail    string
}

// Report は Scan の結果。所見と meta.yml 自動生成の件数
// (dry-run 時は WouldGenerateMeta) を持つ。
type Report struct {
	Findings          []Finding
	MetaGenerated     int
	WouldGenerateMeta int
}

// Scan は全ライセンス (満了含む) について FS と DB を突合する。検査自体は
// 読取専用で、唯一の書込みは meta.yml 欠落時の自動生成 (dryRun なら行わず
// WouldGenerateMeta に数える)。now は生成する meta.yml の
// last_updated_by_app に使う。所見は Report で返し error にしない
// (警告のみでブロックしない思想)。error は動作エラー (DB 不能・walk 失敗
// 等) のみ。
func Scan(ctx context.Context, q *repository.Queries, basePath string, dryRun bool, now time.Time) (Report, error) {
	var rep Report
	// 再利用側 (CLI / 画面) の検証漏れで CWD 相対に meta.yml を書く事故を
	// 防ぐ多層防御。
	if basePath == "" {
		return Report{}, errors.New("file store base path must not be empty")
	}

	rows, err := q.ListLicenses(ctx, 1) // 1 = 満了含む全件
	if err != nil {
		return Report{}, fmt.Errorf("list licenses: %w", err)
	}

	// orphan_dir 判定用: 正常な fs_dir_path の集合 (clean 済み / 区切り)。
	knownDirs := make(map[string]bool, len(rows))

	for _, row := range rows {
		dirAbs, err := licensefs.DirAbs(basePath, row.FsDirPath)
		if err != nil {
			rep.Findings = append(rep.Findings, Finding{
				Kind: KindInvalidPath, LicenseID: row.ID,
				Path: row.FsDirPath, Detail: err.Error(),
			})
			continue // この行の以降の検査はスキップ
		}
		knownDirs[path.Clean(filepath.ToSlash(row.FsDirPath))] = true

		if fi, err := os.Stat(dirAbs); errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()) {
			rep.Findings = append(rep.Findings, Finding{
				Kind: KindDirMissing, LicenseID: row.ID, Path: row.FsDirPath,
			})
			continue // meta / 文書検査はスキップ (復元は generate-meta の責務)
		} else if err != nil {
			return Report{}, fmt.Errorf("stat license dir %s: %w", dirAbs, err)
		}

		scanMeta(ctx, q, basePath, row, dryRun, now, &rep)

		registered, err := scanDocuments(ctx, q, basePath, row, &rep)
		if err != nil {
			return Report{}, err
		}
		if err := scanUnregistered(basePath, dirAbs, row, registered, &rep); err != nil {
			return Report{}, err
		}
	}

	if err := scanOrphanDirs(basePath, knownDirs, &rep); err != nil {
		return Report{}, err
	}
	return rep, nil
}

// scanMeta は meta.yml 欠落を検査し、欠落なら自動生成する (唯一の自動修復。
// dry-run では WouldGenerateMeta に数えるのみ)。生成失敗と存在判定不能は
// 所見 (meta_generate_failed) として続行する。
func scanMeta(ctx context.Context, q *repository.Queries, basePath string, row repository.ListLicensesRow, dryRun bool, now time.Time, rep *Report) {
	metaPath := path.Join(row.FsDirPath, "meta.yml")
	exists, err := licensefs.MetaExists(basePath, row.FsDirPath)
	if err != nil {
		rep.Findings = append(rep.Findings, Finding{
			Kind: KindMetaGenerateFailed, LicenseID: row.ID,
			Path: metaPath, Detail: err.Error(),
		})
		return
	}
	if exists {
		return
	}
	if dryRun {
		rep.WouldGenerateMeta++
		return
	}
	if err := licensefs.Regenerate(ctx, q, basePath, row.ID, now); err != nil {
		rep.Findings = append(rep.Findings, Finding{
			Kind: KindMetaGenerateFailed, LicenseID: row.ID,
			Path: metaPath, Detail: err.Error(),
		})
		return
	}
	rep.MetaGenerated++
}

// scanDocuments は license_documents の各行について実体の存在と sha256 を
// 検査し、正規化済み stored_path の集合 (未登録ファイル判定用) を返す。
func scanDocuments(ctx context.Context, q *repository.Queries, basePath string, row repository.ListLicensesRow, rep *Report) (map[string]bool, error) {
	docs, err := q.ListLicenseDocumentsByLicense(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("list documents (license %d): %w", row.ID, err)
	}
	registered := make(map[string]bool, len(docs))
	for _, doc := range docs {
		// stored_path も DB 由来のため DirAbs と同じガードを通す (多層防御)。
		fileAbs, err := licensefs.DirAbs(basePath, doc.StoredPath)
		if err != nil {
			rep.Findings = append(rep.Findings, Finding{
				Kind: KindInvalidPath, LicenseID: row.ID,
				Path: doc.StoredPath,
				// DirAbs のエラー文言は fs_dir_path を主語にするため、
				// stored_path 由来だと分かる補足を付ける
				Detail: "stored_path: " + err.Error(),
			})
			continue
		}
		registered[path.Clean(filepath.ToSlash(doc.StoredPath))] = true

		// FS が手で差し替えられ極端に大きいファイルになっていても
		// メモリ使用量が一定になるよう、全読みではなくストリーミングで
		// ハッシュする。
		got, err := hashFileSHA256(fileAbs)
		if errors.Is(err, fs.ErrNotExist) {
			rep.Findings = append(rep.Findings, Finding{
				Kind: KindFileMissing, LicenseID: row.ID, Path: doc.StoredPath,
			})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read document %s: %w", fileAbs, err)
		}
		if !strings.EqualFold(got, doc.Sha256) {
			rep.Findings = append(rep.Findings, Finding{
				Kind: KindSha256Mismatch, LicenseID: row.ID, Path: doc.StoredPath,
				Detail: fmt.Sprintf("db=%s fs=%s", doc.Sha256, got),
			})
		}
	}
	return registered, nil
}

// hashFileSHA256 は path の sha256 を小さな固定バッファで計算する。
func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// scanUnregistered は契約フォルダ内の実ファイルのうち、meta.yml でも
// stored_path 群でもないものを unregistered_file として報告する。
func scanUnregistered(basePath, dirAbs string, row repository.ListLicensesRow, registered map[string]bool, rep *Report) error {
	metaRel := path.Join(path.Clean(filepath.ToSlash(row.FsDirPath)), "meta.yml")
	return filepath.WalkDir(dirAbs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk license dir %s (entry %s): %w", dirAbs, p, err)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		relFromBase, err := filepath.Rel(basePath, p)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", p, err)
		}
		rel := filepath.ToSlash(relFromBase)
		if rel == metaRel || registered[rel] {
			return nil
		}
		rep.Findings = append(rep.Findings, Finding{
			Kind: KindUnregisteredFile, LicenseID: row.ID, Path: rel,
		})
		return nil
	})
}

// scanOrphanDirs は licenses/ 直下 3 階層 (vendor/product/license) を走査し、
// 末端ディレクトリが全ライセンスの fs_dir_path 集合に無ければ orphan_dir と
// して報告する (末端のみ報告。§3.2 の構造前提)。
func scanOrphanDirs(basePath string, knownDirs map[string]bool, rep *Report) error {
	root := filepath.Join(basePath, "licenses")
	vendors, err := readDirIfExists(root)
	if err != nil {
		return err
	}
	for _, v := range vendors {
		if !v.IsDir() {
			continue
		}
		products, err := readDirIfExists(filepath.Join(root, v.Name()))
		if err != nil {
			return err
		}
		for _, p := range products {
			if !p.IsDir() {
				continue
			}
			leaves, err := readDirIfExists(filepath.Join(root, v.Name(), p.Name()))
			if err != nil {
				return err
			}
			for _, l := range leaves {
				if !l.IsDir() {
					continue
				}
				rel := path.Join("licenses", v.Name(), p.Name(), l.Name())
				if !knownDirs[rel] {
					rep.Findings = append(rep.Findings, Finding{
						Kind: KindOrphanDir, Path: rel,
					})
				}
			}
		}
	}
	return nil
}

// readDirIfExists は ReadDir の「無ければ空」版。licenses/ 未作成の環境
// (ライセンス 0 件) を walk 失敗にしないため。
func readDirIfExists(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	return entries, nil
}
