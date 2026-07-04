// Package handler は appmgr-server の HTTP ルーティングを組み立てる。
//
// cmd/server/main.go は lock 取得・DB open・signal 処理・Shutdown だけを
// 担当し、ルータ組立とハンドラ実装はすべてこのパッケージに集約する。
package handler

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/filestore"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/session"
	"github.com/tagawa0525/app_man/internal/view/errors"
)

// Deps は NewRouter が必要とする外部依存をまとめる。
type Deps struct {
	Logger   *slog.Logger
	DB       *sql.DB
	StaticFS fs.FS
	// SessionStore は SessionMiddleware が使う永続化境界。
	// 本番では cmd/server/main.go が SQLiteStore を渡す。必須。
	SessionStore session.Store
	// CookieSecure は Set-Cookie の Secure 属性。本番 HTTPS で true、
	// 開発 HTTP で false。config.server.cookie_secure と同義。
	CookieSecure bool
	// SessionMaxAge は新規発行する session の有効期間。
	// config.auth.session_max_age_hours から導出する。
	SessionMaxAge time.Duration
	// Authenticator はログインフロー (POST /login) で利用する。必須。
	Authenticator auth.Authenticator
	// FileStore / FileStoreCfg は証書ファイルの物理配置 (L-3)。
	// cmd/server が file_store.base_path 必須チェックの上で注入する。
	FileStore    *filestore.Store
	FileStoreCfg config.FileStoreConfig
}

// NewRouter は appmgr-server で使う http.Handler を組み立てる。
//
// middleware チェーン: RequestID → recoverer → SessionMiddleware →
// AuthMiddleware → CSRFMiddleware。
// 公開パス (/healthz, /static/*, /login, /logout) は AuthMiddleware が
// 素通りさせる (デフォルト PublicPathPrefixes と LoginURL.Path の合成)。
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(recoverer(deps.Logger))
	r.Use(middleware.SessionMiddleware(middleware.SessionConfig{
		Store:        deps.SessionStore,
		SecureCookie: deps.CookieSecure,
		MaxAge:       deps.SessionMaxAge,
		Logger:       deps.Logger,
	}))
	r.Use(middleware.AuthMiddleware(middleware.AuthConfig{
		DB:     deps.DB,
		Logger: deps.Logger,
	}))
	r.Use(middleware.CSRFMiddleware)

	r.Get("/healthz", healthHandler)

	if deps.StaticFS != nil {
		fileServer := http.FileServer(http.FS(deps.StaticFS))
		r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	}

	web.RegisterRoutes(r, web.Deps{
		Logger:        deps.Logger,
		DB:            deps.DB,
		Authenticator: deps.Authenticator,
		SessionStore:  deps.SessionStore,
		CookieSecure:  deps.CookieSecure,
		SessionMaxAge: deps.SessionMaxAge,
		FileStore:     deps.FileStore,
		FileStoreCfg:  deps.FileStoreCfg,
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
