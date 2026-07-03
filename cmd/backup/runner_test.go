package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
)

// newSourceDB は本番と同構成 (WAL 有効 + foreign_keys) のソース DB を
// 作成し、items テーブルに rows 件 INSERT して閉じ、DB パスを返す。
func newSourceDB(t *testing.T, rows int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	sqlDB, closeDB, err := db.Open(config.DatabaseConfig{Path: path, WAL: true})
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < rows; i++ {
		if _, err := sqlDB.Exec(`INSERT INTO items (name) VALUES (?)`, fmt.Sprintf("item-%d", i)); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if err := closeDB(); err != nil {
		t.Fatalf("close source db: %v", err)
	}
	return path
}

// newDeps は runBackup 用の clirun.Deps を組み立てる。ログはテストでは
// 検証しないので破棄する。
func newDeps(dbPath, outputDir string, generations int) clirun.Deps {
	return clirun.Deps{
		Cfg: &config.Config{
			Database: config.DatabaseConfig{Path: dbPath, WAL: true},
			Backup:   config.BackupConfig{OutputDir: outputDir, Generations: generations},
		},
		Logger: slog.New(slog.DiscardHandler),
	}
}

// countItems はバックアップ出力を読み取り専用の完成品として直接開き、
// items の件数を返す。db.Open は WAL 切替の書込みを伴うため使わない。
func countItems(t *testing.T, path string) int {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open backup %s: %v", path, err)
	}
	defer func() { _ = sqlDB.Close() }()

	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("count items in %s: %v", path, err)
	}
	return n
}

// assertNoTmpFiles は dir 内に .tmp で終わるファイルが 1 つも無いことを確認する。
// 部分ファイル (tmp) が dest 名で残ると次回の VACUUM INTO が恒久ブロック
// されるため、成功時・失敗時とも tmp が残らないことが受け入れ基準。
func assertNoTmpFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file left behind: %s", e.Name())
		}
	}
}

// TestRunBackup_CreatesValidSQLite は正常系:
//   - OutputDir が無ければ MkdirAll で作られる
//   - app-<YYYYMMDD-HHMMSS>.db が出力され、有効な SQLite として
//     開いて SELECT でき、件数がソース DB と一致する
//   - .tmp ファイルが残らない
func TestRunBackup_CreatesValidSQLite(t *testing.T) {
	t.Parallel()

	const rows = 3
	srcPath := newSourceDB(t, rows)
	// MkdirAll の検証を兼ねて、存在しないサブディレクトリを出力先にする。
	outputDir := filepath.Join(t.TempDir(), "backups")
	deps := newDeps(srcPath, outputDir, 0)
	now := time.Date(2026, 7, 3, 15, 4, 5, 0, time.UTC)

	if err := runBackup(context.Background(), deps, now); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	dest := filepath.Join(outputDir, "app-20260703-150405.db")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	if got := countItems(t, dest); got != rows {
		t.Errorf("backup row count: want %d, got %d", rows, got)
	}
	assertNoTmpFiles(t, outputDir)
}
