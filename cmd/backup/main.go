// appmgr-backup: SQLite を VACUUM INTO で backup.output_dir に書き出す
// (要件書 § 8.4)。<dest>.tmp に書いて fsync 後に rename する原子的な出力で
// 「app-*.db は常に完成品」を保証し、backup.generations で世代管理する。
// 実処理は runner.go の runBackup。
// ModeGlobal で他全バッチの lock を相互排他取得する（VACUUM INTO 中の
// 書込み衝突を防ぐため、要件書 § 8.8）。
package main

import (
	"context"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-backup"

func main() {
	clirun.Run(binaryName, lockfile.ModeGlobal, func(ctx context.Context, deps clirun.Deps) error {
		return runBackup(ctx, deps, time.Now())
	})
}
