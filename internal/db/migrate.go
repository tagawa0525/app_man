package db

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	sqlitedrv "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/tagawa0525/app_man/db/migrations"
)

func newMigrator(sqlDB *sql.DB) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("iofs.New: %w", err)
	}

	drv, err := sqlitedrv.WithInstance(sqlDB, &sqlitedrv.Config{})
	if err != nil {
		return nil, fmt.Errorf("sqlite.WithInstance: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return nil, fmt.Errorf("migrate.NewWithInstance: %w", err)
	}
	return m, nil
}

// MigrateUp は embed された全マイグレーションを順に適用する。
// 既に最新版で何も変わらない場合もエラーを返さない。
func MigrateUp(sqlDB *sql.DB) error {
	m, err := newMigrator(sqlDB)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateDown は適用済みマイグレーションを逆順に巻き戻し、
// すべての DDL を DROP する。
func MigrateDown(sqlDB *sql.DB) error {
	m, err := newMigrator(sqlDB)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// RequiredMigrationVersion は appmgr-server が要求する
// スキーマ版数を返す。マイグレーションファイルを追加した際は
// この値も更新する。
func RequiredMigrationVersion() uint {
	return 6
}

// CheckVersion は現在の DB スキーマ版数が RequiredMigrationVersion
// と一致するか確認する。未初期化・不一致・dirty 状態のいずれでも
// エラーを返し、appmgr-server はそれを受けて起動失敗する想定。
// マイグレーションの自動適用は行わない (誤デプロイ防止)。
func CheckVersion(sqlDB *sql.DB) error {
	m, err := newMigrator(sqlDB)
	if err != nil {
		return err
	}

	current, dirty, err := m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return fmt.Errorf("schema not initialized: run `make migrate-up`")
		}
		return fmt.Errorf("get current schema version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema is dirty at version %d (previous migration failed; manual fix required)", current)
	}
	required := RequiredMigrationVersion()
	if current != required {
		return fmt.Errorf("schema version mismatch: got %d, want %d (run `make migrate-up`)", current, required)
	}
	return nil
}
