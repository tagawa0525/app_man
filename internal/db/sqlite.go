package db

import (
	"database/sql"

	"github.com/tagawa0525/app_man/internal/config"
)

// Open は modernc.org/sqlite で SQLite に接続し、WAL モードと
// foreign_keys ON の PRAGMA を適用した *sql.DB と Close 用関数を返す。
// 呼び出し側は closeFn を defer して解放する。
func Open(cfg config.DatabaseConfig) (*sql.DB, func() error, error) {
	panic("not implemented")
}

// applyPragmas は接続済み DB に必須 PRAGMA を適用する。
// (テストから直接 PRAGMA を確認するため、Open 内ではなくここで分離)
func applyPragmas(db *sql.DB, wal bool) error {
	_ = db
	_ = wal
	panic("not implemented")
}
