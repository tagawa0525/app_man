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
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/view/errors"
)

// Deps は NewRouter が必要とする外部依存をまとめる。
// フェーズ 3 でセッションストア・CSRF ジェネレータ・Authenticator を追加する。
type Deps struct {
	Logger   *slog.Logger
	DB       *sql.DB
	StaticFS fs.FS
	// DevMode は開発用エンドポイント (POST /__set_role 等) を有効化する
	// フラグ。本番では false にして、外部から system_admin に自己昇格
	// される経路を完全に塞ぐ。cmd/server/main.go で APP_MAN_DEV_MODE
	// 環境変数から読む。
	DevMode bool
}

// NewRouter は appmgr-server で使う http.Handler を組み立てる。
//
// PR-A では /healthz と /static/* のみ登録する。
// 業務ハンドラは PR-B 以降で追加する。
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(recoverer(deps.Logger))
	r.Use(middleware.DummyAuthMiddleware)
	r.Use(middleware.CSRFMiddleware)

	r.Get("/healthz", healthHandler)

	if deps.StaticFS != nil {
		fileServer := http.FileServer(http.FS(deps.StaticFS))
		r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	}

	web.RegisterRoutes(r, web.Deps{
		Logger:  deps.Logger,
		DB:      deps.DB,
		DevMode: deps.DevMode,
	})

	r.NotFound(notFoundHandler)

	return r
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	role := middleware.RoleFrom(r.Context())
	if err := errors.NotFound(role).Render(r.Context(), w); err != nil {
		// レンダリング失敗時は素の 404 を返すしかない (status は既に書き込み済)。
		_, _ = w.Write([]byte("404 not found"))
	}
}
