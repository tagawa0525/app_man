package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// runGenerateMeta は db.Open + generateAll の薄い合成。now は meta.yml の
// last_updated_by_app に使う (main が time.Now() を渡す)。テストは
// in-memory DB を注入できる generateAll を直接呼ぶため、この関数はテスト
// 対象にしない。
func runGenerateMeta(ctx context.Context, deps clirun.Deps, now time.Time) error {
	// BasePath の必須チェックは config.validate ではなくここで行う。
	// validate で必須化すると file_store 設定を持たないバッチ等が起動
	// 不能になるため、バイナリ固有の前提条件として検査する
	// (backup.output_dir と同じ消費者責務パターン)。
	basePath := deps.Cfg.FileStore.BasePath
	if basePath == "" {
		return errors.New("file_store.base_path is required")
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
	return generateAll(ctx, sqlDB, basePath, deps.Logger, now, deps.DryRun)
}

// generateAll は全ライセンス (満了含む。仕様 §5.2「満了レコードも証書・
// meta.yml は保持」) の物理ディレクトリ確保と meta.yml 再生成の本体。
// runGenerateMeta から切り離してあるのは、テストが handlertest.NewTestDB の
// in-memory DB を注入できる継ぎ目を作るため (prune-logs の pruneAll と同流儀)。
//
// 1 件の失敗で中断せず全件処理する (一括再生成の目的 = できる限りの復旧)。
// 失敗が 1 件以上なら error を返し、exit 1 でスケジューラ / 運用者に通知する。
func generateAll(ctx context.Context, sqlDB *sql.DB, basePath string, logger *slog.Logger, now time.Time, dryRun bool) error {
	q := repository.New(sqlDB)
	rows, err := q.ListLicenses(ctx, 1) // 1 = 満了含む全件
	if err != nil {
		return fmt.Errorf("list licenses: %w", err)
	}

	if dryRun {
		wouldCreate := 0
		failed := 0
		for _, row := range rows {
			exists, err := licensefs.MetaExists(basePath, row.FsDirPath)
			if err != nil {
				// 汚染された fs_dir_path (basePath 脱出等) を黙って無視しない。
				// 実行時も同じ行が失敗するため、failed として予告する。
				logger.Error("resolve license dir",
					slog.Int64("license_id", row.ID),
					slog.Any("error", err),
				)
				failed++
				continue
			}
			if !exists {
				wouldCreate++
			}
		}
		logger.Info("generate-meta dry-run",
			slog.Int("total", len(rows)),
			slog.Int("would_create", wouldCreate),
			slog.Int("failed", failed),
		)
		if failed > 0 {
			return fmt.Errorf("generate-meta dry-run: %d of %d licenses failed", failed, len(rows))
		}
		return nil
	}

	failed := 0
	for _, row := range rows {
		if err := licensefs.Regenerate(ctx, q, basePath, row.ID, now); err != nil {
			// ライセンスの中身 (キー等) はログに出さない。ID とエラーのみ (§8.5)。
			logger.Error("regenerate license fs",
				slog.Int64("license_id", row.ID),
				slog.Any("error", err),
			)
			failed++
		}
	}

	logger.Info("generate-meta completed",
		slog.Int("total", len(rows)),
		slog.Int("succeeded", len(rows)-failed),
		slog.Int("failed", failed),
	)
	if failed > 0 {
		return fmt.Errorf("generate-meta: %d of %d licenses failed", failed, len(rows))
	}
	return nil
}
