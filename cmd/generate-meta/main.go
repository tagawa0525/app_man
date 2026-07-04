// appmgr-generate-meta: 全ライセンス (満了含む) の契約フォルダについて
// 物理ディレクトリを確保し meta.yml を DB の現在内容で一括再生成する
// (仕様 §5.2 / §9、必要時実行)。--dry-run で対象件数 (total / would_create)
// のみ確認できる。フラグパース・config・logger・lock の共通起動は clirun に
// 委譲する。
package main

import (
	"context"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-generate-meta"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(ctx context.Context, deps clirun.Deps) error {
		return runGenerateMeta(ctx, deps, time.Now())
	})
}
