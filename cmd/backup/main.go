// appmgr-backup: SQLite を VACUUM INTO で別ファイルに書き出す。
// ModeGlobal で他全バッチの lock を相互排他取得する（VACUUM INTO 中の
// 書込み衝突を防ぐため、要件書 § 8.8）。
// 実処理はフェーズ 1 PR3 では未実装。clirun に共通起動を委譲する骨格のみ。
package main

import (
	"context"
	"log/slog"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-backup"

func main() {
	clirun.Run(binaryName, lockfile.ModeGlobal, func(_ context.Context, deps clirun.Deps) error {
		deps.Logger.Info("not implemented (skeleton only)",
			slog.Bool("dry_run", deps.DryRun))
		return nil
	})
}
