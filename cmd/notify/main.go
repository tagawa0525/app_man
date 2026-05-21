// appmgr-notify: 日次通知メール送信と失敗分の再送を行う。
// 実処理はフェーズ 1 PR3 では未実装。clirun に共通起動を委譲する骨格のみ。
package main

import (
	"context"
	"log/slog"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-notify"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(_ context.Context, deps clirun.Deps) error {
		deps.Logger.Info("not implemented (skeleton only)",
			slog.Bool("dry_run", deps.DryRun))
		return nil
	})
}
