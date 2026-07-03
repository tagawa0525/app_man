package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/db"
)

// runBackup は SQLite を VACUUM INTO で OutputDir に書き出す本体。
// now は出力ファイル名のタイムスタンプに使う (main が time.Now() を渡し、
// テストは固定時刻を注入する)。
//
// VACUUM INTO は既存ファイルがあるとエラーになる仕様のため、いったん
// <dest>.tmp に書いてから rename する。中断で dest 名の部分ファイルが
// 残ると次回実行が恒久ブロックされるのを防ぎ、「app-*.db は常に完成品」
// を保証するため。
func runBackup(ctx context.Context, deps clirun.Deps, now time.Time) error {
	// OutputDir の必須チェックは config.validate ではなくここで行う。
	// validate で必須化すると backup 設定を持たない server 等が起動不能に
	// なるため、バイナリ固有の前提条件として検査する。
	outputDir := deps.Cfg.Backup.OutputDir
	if outputDir == "" {
		return errors.New("backup.output_dir is required")
	}

	// SQLite は open 時に空 DB を自動生成するため、db.Open の前にソース DB の
	// 存在を確認する。database.path の設定ミスのまま「空 DB のバックアップに
	// 成功」する事故を防ぐ。
	if _, err := os.Stat(deps.Cfg.Database.Path); err != nil {
		return fmt.Errorf("source database: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// タイムスタンプはローカル時刻 (JST)。辞書順 = 時刻順になる形式。
	dest := filepath.Join(outputDir, "app-"+now.Format("20060102-150405")+".db")
	tmp := dest + ".tmp"

	// dry-run: tmp 掃除・VACUUM INTO・世代管理の削除を一切行わず、
	// 実行した場合の出力先と削除予定 (dest が出来たと仮定) をログに出す。
	if deps.DryRun {
		return dryRunBackup(deps, outputDir, dest)
	}

	// 前回中断の残骸 tmp を掃除する。ModeGlobal lock 下なので並走
	// プロセスの tmp を消す危険は無い。
	if err := removeStaleTmp(outputDir); err != nil {
		return err
	}

	sqlDB, closeDB, err := db.Open(deps.Cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if cerr := closeDB(); cerr != nil {
			deps.Logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	// パス文字列の SQL 連結を避けるためパラメータバインドで渡す
	// (modernc.org/sqlite で動作確認済み)。
	if _, err := sqlDB.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		// 部分ファイルを残すと (dest 名なら) 次回の VACUUM INTO を恒久
		// ブロックするため削除する。tmp 未作成のまま失敗した場合もある。
		if rmErr := os.Remove(tmp); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			deps.Logger.Error("remove partial tmp", slog.String("path", tmp), slog.Any("error", rmErr))
		}
		return fmt.Errorf("VACUUM INTO: %w", err)
	}

	// SQLite の VACUUM INTO は出力を sync しないため、rename 前に明示的に
	// fsync する (電源断でのバックアップ破損防止)。dir の fsync は Windows
	// 非対応のため行わない。
	if err := syncFile(tmp); err != nil {
		if rmErr := os.Remove(tmp); rmErr != nil {
			deps.Logger.Error("remove tmp after sync failure", slog.String("path", tmp), slog.Any("error", rmErr))
		}
		return err
	}

	if err := os.Rename(tmp, dest); err != nil {
		if rmErr := os.Remove(tmp); rmErr != nil {
			deps.Logger.Error("remove tmp after rename failure", slog.String("path", tmp), slog.Any("error", rmErr))
		}
		return fmt.Errorf("rename tmp to dest: %w", err)
	}

	pruned, err := pruneOldBackups(outputDir, deps.Cfg.Backup.Generations)
	if err != nil {
		return err
	}

	fi, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf("stat dest: %w", err)
	}
	deps.Logger.Info("backup completed",
		slog.String("dest", dest),
		slog.Int64("size_bytes", fi.Size()),
		slog.Int("pruned_count", pruned),
	)
	return nil
}

// backupNamePattern は完成品バックアップのファイル名。glob (app-*.db) では
// なく厳格一致にするのは、利用者が置いた無関係ファイル (例 app-old.db) を
// 世代管理で誤削除しないため。staleTmpPattern はその書きかけ (前回中断の
// 残骸) で、同じ理由で厳格一致にする。
var (
	backupNamePattern = regexp.MustCompile(`^app-\d{8}-\d{6}\.db$`)
	staleTmpPattern   = regexp.MustCompile(`^app-\d{8}-\d{6}\.db\.tmp$`)
)

// dryRunBackup は破壊的操作を行わず、出力予定の dest と、dest が出来たと
// 仮定した場合の世代管理の削除予定をログに出す。実行後の姿を予告する方が
// 事前確認として有用なため、既存分だけでなく dest を含めて算出する。
func dryRunBackup(deps clirun.Deps, outputDir, dest string) error {
	names, err := listMatching(outputDir, backupNamePattern)
	if err != nil {
		return err
	}
	base := filepath.Base(dest)
	if !slices.Contains(names, base) {
		names = append(names, base)
		slices.Sort(names)
	}
	wouldPrune := prunePlan(names, deps.Cfg.Backup.Generations)
	deps.Logger.Info("backup dry-run",
		slog.String("dest", dest),
		slog.Any("would_prune", wouldPrune),
	)
	return nil
}

// removeStaleTmp は dir 内の前回中断が残した app-*.db.tmp (厳格パターン
// 一致) を削除する。dest 名の部分ファイルは次回の VACUUM INTO を恒久
// ブロックするため、実行冒頭で必ず掃除する。
func removeStaleTmp(dir string) error {
	names, err := listMatching(dir, staleTmpPattern)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("remove stale tmp: %w", err)
		}
	}
	return nil
}

// pruneOldBackups は dir 内の ^app-\d{8}-\d{6}\.db$ に一致するファイルを
// 名前昇順 (= 時刻昇順) に並べ、新しい方から generations 個を残して
// 古いものを削除し、削除数を返す。generations == 0 は no-op。
func pruneOldBackups(dir string, generations int) (int, error) {
	names, err := listMatching(dir, backupNamePattern)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, name := range prunePlan(names, generations) {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("remove old backup: %w", err)
		}
		removed++
	}
	return removed, nil
}

// listMatching は dir 直下で pattern に一致する通常ファイル名を昇順で返す。
func listMatching(dir string, pattern *regexp.Regexp) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read output dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !pattern.MatchString(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names, nil
}

// prunePlan は昇順の names のうち、新しい方から generations 個を残した
// 削除対象 (古い方) を返す。generations == 0 は無制限保持で対象なし。
func prunePlan(names []string, generations int) []string {
	if generations == 0 || len(names) <= generations {
		return nil
	}
	return names[:len(names)-generations]
}

// syncFile は path を開いて fsync し、閉じる。VACUUM INTO の出力を
// ディスクに永続化するために使う。
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close after sync: %w", err)
	}
	return nil
}
