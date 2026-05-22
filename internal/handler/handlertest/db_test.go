package handlertest_test

import (
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
)

func TestNewTestDB_appliesMigrations(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)

	const q = `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name != 'schema_migrations'`
	var n int
	if err := sqlDB.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n < 24 {
		t.Errorf("user tables = %d, want >= 24", n)
	}
}

func TestNewTestDB_isolated(t *testing.T) {
	t.Parallel()

	a := handlertest.NewTestDB(t)
	b := handlertest.NewTestDB(t)

	if _, err := a.Exec("INSERT INTO vendors(name) VALUES('vendor-a')"); err != nil {
		t.Fatalf("insert into a: %v", err)
	}

	var n int
	if err := b.QueryRow("SELECT COUNT(*) FROM vendors WHERE name='vendor-a'").Scan(&n); err != nil {
		t.Fatalf("count in b: %v", err)
	}
	if n != 0 {
		t.Errorf("vendors from db a leaked into db b (count=%d)", n)
	}
}
