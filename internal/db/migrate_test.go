package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
)

func TestMigrate_upDownRoundTrip(t *testing.T) {
	cfg := config.DatabaseConfig{
		Path: filepath.Join(t.TempDir(), "test.db"),
		WAL:  true,
	}
	sqlDB, closeFn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	if err := db.MigrateUp(sqlDB); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	if got := countUserTables(t, sqlDB); got < 24 {
		t.Errorf("after up: user tables = %d, want >= 24", got)
	}

	if got := countViews(t, sqlDB, "v_license_usage"); got != 1 {
		t.Errorf("after up: v_license_usage views = %d, want 1", got)
	}

	if err := db.MigrateDown(sqlDB); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}

	if got := countUserTables(t, sqlDB); got != 0 {
		t.Errorf("after down: user tables = %d, want 0", got)
	}
	if got := countViews(t, sqlDB, "v_license_usage"); got != 0 {
		t.Errorf("after down: v_license_usage views = %d, want 0", got)
	}
}

// countUserTables は schema_migrations と sqlite_* を除いたユーザテーブル数を返す。
func countUserTables(t *testing.T, sqlDB *sql.DB) int {
	t.Helper()
	const q = `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name != 'schema_migrations'
	`
	var n int
	if err := sqlDB.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	return n
}

func countViews(t *testing.T, sqlDB *sql.DB, name string) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='view' AND name=?", name,
	).Scan(&n); err != nil {
		t.Fatalf("count views: %v", err)
	}
	return n
}

func TestRequiredMigrationVersion(t *testing.T) {
	if got := db.RequiredMigrationVersion(); got != 6 {
		t.Errorf("RequiredMigrationVersion() = %d, want 6", got)
	}
}

func TestCheckVersion_failsOnStaleSchema(t *testing.T) {
	cfg := config.DatabaseConfig{
		Path: filepath.Join(t.TempDir(), "test.db"),
		WAL:  true,
	}
	sqlDB, closeFn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	// 未初期化: error を期待
	if err := db.CheckVersion(sqlDB); err == nil {
		t.Error("CheckVersion on uninitialized schema: want error, got nil")
	}

	// 完全 up 後: nil を期待
	if err := db.MigrateUp(sqlDB); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	if err := db.CheckVersion(sqlDB); err != nil {
		t.Errorf("CheckVersion after MigrateUp: want nil, got %v", err)
	}
}
