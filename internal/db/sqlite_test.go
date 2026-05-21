package db_test

import (
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
)

func TestOpen_setsWALAndForeignKeys(t *testing.T) {
	cfg := config.DatabaseConfig{
		Path: filepath.Join(t.TempDir(), "test.db"),
		WAL:  true,
	}

	sqlDB, closeFn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := closeFn(); cerr != nil {
			t.Errorf("closeFn: %v", cerr)
		}
	})

	var journalMode string
	if err := sqlDB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var fkEnabled int
	if err := sqlDB.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("foreign_keys = %d, want 1", fkEnabled)
	}
}

func TestOpen_walFalseUsesDeleteMode(t *testing.T) {
	cfg := config.DatabaseConfig{
		Path: filepath.Join(t.TempDir(), "test.db"),
		WAL:  false,
	}

	sqlDB, closeFn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	var journalMode string
	if err := sqlDB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode == "wal" {
		t.Errorf("journal_mode = %q, expected non-wal when WAL=false", journalMode)
	}
}
