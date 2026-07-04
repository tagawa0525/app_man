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
	"github.com/tagawa0525/app_man/internal/integrity"
	"github.com/tagawa0525/app_man/internal/repository"
)

// runCheckIntegrity は db.Open + checkIntegrity の薄い合成。now は meta.yml
// 自動生成の last_updated_by_app に使う (main が time.Now() を渡す)。テストは
// in-memory DB を注入できる checkIntegrity を直接呼ぶため、この関数はテスト
// 対象にしない (generate-meta の runGenerateMeta と同流儀)。
func runCheckIntegrity(ctx context.Context, deps clirun.Deps, now time.Time) error {
	// BasePath の必須チェックはバイナリ固有の前提条件としてここで行う
	// (generate-meta と同じ消費者責務パターン)。
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
	return checkIntegrity(ctx, sqlDB, basePath, deps.Logger, now, deps.DryRun)
}

// checkIntegrity は integrity.Scan を実行し、所見 1 件ごとに warn、最後に
// kind 別サマリを info でログする。所見があっても nil を返す (exit 0)。
// 警告はブロックしない思想 (仕様 §5.12、FS が正本) のため、error は
// Scan 自体の動作エラー (DB 不能・walk 失敗等) のみ。
func checkIntegrity(ctx context.Context, sqlDB *sql.DB, basePath string, logger *slog.Logger, now time.Time, dryRun bool) error {
	rep, err := integrity.Scan(ctx, repository.New(sqlDB), basePath, dryRun, now)
	if err != nil {
		return fmt.Errorf("integrity scan: %w", err)
	}

	counts := make(map[string]int, len(integrity.Kinds))
	for _, f := range rep.Findings {
		counts[f.Kind]++
		logger.Warn("integrity finding",
			slog.String("kind", f.Kind),
			slog.Int64("license_id", f.LicenseID),
			slog.String("path", f.Path),
			slog.String("detail", f.Detail),
		)
	}

	attrs := []any{
		slog.Bool("dry_run", dryRun),
		slog.Int("total_findings", len(rep.Findings)),
		slog.Int("meta_generated", rep.MetaGenerated),
		slog.Int("would_generate_meta", rep.WouldGenerateMeta),
	}
	for _, kind := range integrity.Kinds {
		attrs = append(attrs, slog.Int(kind, counts[kind]))
	}
	logger.Info("check-integrity completed", attrs...)
	return nil
}
