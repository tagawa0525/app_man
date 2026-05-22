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

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
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
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(middleware.DummyAuthMiddleware)
	r.Use(middleware.CSRFMiddleware)

	r.Get("/healthz", healthHandler)

	if deps.StaticFS != nil {
		fileServer := http.FileServer(http.FS(deps.StaticFS))
		r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	}

	return r
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
