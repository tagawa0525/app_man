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
