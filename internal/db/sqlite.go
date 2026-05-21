package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/tagawa0525/app_man/internal/config"
)

// Open は modernc.org/sqlite で SQLite に接続する。
// foreign_keys は DSN の _pragma で接続単位に適用するため pool 内の
// 全接続で有効。WAL は DB ファイル全体の設定なので接続後 1 回だけ
// PRAGMA で切り替える。返す closeFn は *sql.DB.Close をそのまま指す。
func Open(cfg config.DatabaseConfig) (*sql.DB, func() error, error) {
	dsn := cfg.Path + "?_pragma=foreign_keys(1)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("sql.Open: %w", err)
	}

	if cfg.WAL {
		if _, err := sqlDB.Exec("PRAGMA journal_mode = WAL"); err != nil {
			_ = sqlDB.Close()
			return nil, nil, fmt.Errorf("PRAGMA journal_mode=WAL: %w", err)
		}
	}

	return sqlDB, sqlDB.Close, nil
}
