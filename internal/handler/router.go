// Package handler は appmgr-server の HTTP ルーティングを組み立てる。
//
// cmd/server/main.go は lock 取得・DB open・signal 処理・Shutdown だけを
// 担当し、ルータ組立とハンドラ実装はすべてこのパッケージに集約する。
// 後続 PR (PR-B 以降) で /products, /departments 等を NewRouter 内に
// 追加していく。
package handler

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
)

// Deps は NewRouter が必要とする外部依存をまとめる。
// フェーズ 3 でセッションストア・CSRF ジェネレータ・Authenticator を追加する。
type Deps struct {
	Logger   *slog.Logger
	DB       *sql.DB
	StaticFS fs.FS
}

// NewRouter は appmgr-server で使う http.Handler を組み立てる。
//
// PR-A では /healthz と /static/* のみ登録する。
// 業務ハンドラは PR-B 以降で追加する。
func NewRouter(_ Deps) http.Handler {
	// stub: 実装は次コミットで入れる
	return http.NewServeMux()
}
