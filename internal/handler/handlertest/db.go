package handlertest

import (
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"

	// modernc.org/sqlite を database/sql に登録する (driver name "sqlite")。
	// 本パッケージは handler の統合テスト専用なので、CGO 不要の pure-Go
	// ドライバを直接 import する。
	_ "modernc.org/sqlite"

	"github.com/tagawa0525/app_man/internal/db"
)

// testDBCounter はテスト関数毎に一意な in-memory DB 名を割り当てるカウンタ。
//
// SQLite の cache=shared は名前付き memory DB を pool 内の全接続で共有する
// 仕組みのため、同じ名前のままだと並列テストで状態が混ざる。テスト 1 件 =
// DB 1 個になるよう、open する毎にインクリメントして DSN に埋める。
var testDBCounter int64

// NewTestDB は in-memory sqlite を開き、全マイグレーションを適用した上で
// *sql.DB を返す。テスト終了時に Close される。
//
// 各呼び出しは独立した in-memory インスタンスを返すため (t.Parallel() でも
// 衝突しない)、handler 統合テストはこのヘルパを 1 行呼ぶだけで「24 テーブル
// + ビュー」がそろった DB を手に入れられる。
func NewTestDB(t *testing.T) *sql.DB {
	t.Helper()
	id := atomic.AddInt64(&testDBCounter, 1)
	dsn := fmt.Sprintf("file:appmgr_test_%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", id)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.MigrateUp(sqlDB); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	return sqlDB
}
