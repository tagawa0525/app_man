package export

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// snapshotEntryName は ZIP 内の DB スナップショットのエントリ名。
const snapshotEntryName = "db-snapshot.db"

// WriteZip は (1) VACUUM INTO で取った DB スナップショットと
// (2) basePath/licenses/ ツリー全量 (証書 + meta.yml) を w に ZIP として
// 書く (仕様 §5.10「DB スナップショット + 全証書 + meta.yml」)。
//
// VACUUM INTO は稼働中 DB から整合の取れた完成品を得る唯一の安全手段
// (cmd/backup と同方式)。既存ファイルがあるとエラーになる仕様のため、
// os.MkdirTemp で作った専用ディレクトリ内の未作成パスへ書き出し、defer で
// ディレクトリごと消す (サーバの作業領域を汚さない・パスは応答に出さない)。
//
// VACUUM INTO の失敗 (appmgr-backup と並走した SQLITE_BUSY 等) は w へ
// 1 バイトも書かずに返すので、呼び出し側は 500 応答に切り替えられる。
// licenses/ ディレクトリが存在しない場合はスナップショットのみの ZIP に
// なる (FS が空でもブロックしない)。
func WriteZip(ctx context.Context, db *sql.DB, basePath string, w io.Writer) error {
	tmpDir, err := os.MkdirTemp("", "appmgr-export-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// パス文字列の SQL 連結を避けるためパラメータバインドで渡す
	// (cmd/backup で modernc.org/sqlite での動作確認済み)。
	snapshot := filepath.Join(tmpDir, snapshotEntryName)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("VACUUM INTO: %w", err)
	}

	// 途中でエラーになった場合は zw.Close() せずに返す: central directory
	// を書かない切断された ZIP になり、読み手が「壊れた完成品」を有効な
	// アーカイブと取り違えない。
	zw := zip.NewWriter(w)
	if err := addFile(zw, snapshot, snapshotEntryName); err != nil {
		return err
	}
	if err := addLicenseTree(zw, basePath); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize zip: %w", err)
	}
	return nil
}

// addLicenseTree は basePath/licenses 配下の通常ファイルを basePath 相対の
// スラッシュ区切りパス (licenses/<vendor>/<product>/<license>/...) で zw に
// 追加する。ディレクトリ自体が無ければ何もしない。
func addLicenseTree(zw *zip.Writer, basePath string) error {
	root := filepath.Join(basePath, "licenses")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat licenses dir: %w", err)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk licenses dir: %w", err)
		}
		// 通常ファイルのみ格納する (空ディレクトリのエントリは作らない。
		// symlink は FS 正本の運用で想定しないため辿らない)。
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(basePath, path)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", path, err)
		}
		return addFile(zw, path, filepath.ToSlash(rel))
	})
}

// addFile は path の内容を name というエントリ名で zw に追加する。
func addFile(zw *zip.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	entry, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := io.Copy(entry, f); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}
