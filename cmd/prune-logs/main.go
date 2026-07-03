// appmgr-prune-logs: app_settings の保持期間キー (仕様書 §5.11) に従い、
// 保持期間を超過した audit_logs / raw_installations / import_logs /
// notifications (送信済みのみ) を物理削除する。--dry-run で対象件数のみ
// 確認できる。フラグパース・config・logger・lock の共通起動は clirun に委譲する。
package main

import (
	"context"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-prune-logs"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(ctx context.Context, deps clirun.Deps) error {
		return runPrune(ctx, deps, time.Now())
	})
}
