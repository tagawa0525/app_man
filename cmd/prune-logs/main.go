// appmgr-prune-logs: 保持期間超過レコード（audit_logs / raw_installations 等）を
// 物理削除する。実処理はフェーズ 1 PR3 では未実装。clirun に共通起動を委譲する骨格のみ。
package main

import (
	"context"
	"log/slog"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-prune-logs"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(_ context.Context, deps clirun.Deps) error {
		deps.Logger.Info("not implemented (skeleton only)",
			slog.Bool("dry_run", deps.DryRun))
		return nil
	})
}
