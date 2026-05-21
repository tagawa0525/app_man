// appmgr-generate-meta: ライセンス契約フォルダの meta.yml を一括再生成する。
// 実処理はフェーズ 1 PR3 では未実装。clirun に共通起動を委譲する骨格のみ。
package main

import (
	"context"
	"log/slog"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-generate-meta"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(_ context.Context, deps clirun.Deps) error {
		deps.Logger.Info("not implemented (skeleton only)",
			slog.Bool("dry_run", deps.DryRun))
		return nil
	})
}
