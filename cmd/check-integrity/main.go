// appmgr-check-integrity: FS (ライセンス契約フォルダ) と DB の整合性を
// チェックする (仕様 §5.12)。所見は warn ログ + kind 別サマリで報告し、
// あっても exit 0 (警告のみでブロックしない思想)。唯一の自動修復は
// meta.yml 欠落時の生成で、--dry-run では行わず件数のみ報告する。
// フラグパース・config・logger・lock の共通起動は clirun に委譲する。
package main

import (
	"context"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-check-integrity"

func main() {
	clirun.Run(binaryName, lockfile.ModeShared, func(ctx context.Context, deps clirun.Deps) error {
		return runCheckIntegrity(ctx, deps, time.Now())
	})
}
