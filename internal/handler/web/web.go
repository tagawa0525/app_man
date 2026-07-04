// Package web は business handler (templ + HTMX + Cookie セッション認証
// 系) のエントリポイント。要件書 §8.9 で定められた web/api 分離の web 側。
//
// internal/handler/router.go から RegisterRoutes(r, deps) が呼ばれ、
// /vendors, /products 等の業務ルートを r に登録する。本 PR (フェーズ 2
// PR-B) で初投入され、PR-C 以降の departments / users / devices も同じ
// パッケージにファイルを追加する形で拡張する。
package web

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/auth"
	mw "github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// Deps は web ハンドラが必要とする外部依存。internal/handler の Deps と
// 同型だが、循環 import を避けるため web パッケージ独自に定義する。
type Deps struct {
	Logger *slog.Logger
	DB     *sql.DB
	// Authenticator はログインフロー (POST /login) が利用する。
	// 本 PR では LocalAuthenticator が直接渡る。
	Authenticator auth.Authenticator
	// SessionStore はログアウト時の session 削除 / login 後のクエリ等で使う。
	SessionStore session.Store
	// CookieSecure はログイン後の Cookie 再発行 / ログアウト時の Cookie 消去に使う。
	CookieSecure bool
	// SessionMaxAge はログイン後に session ID を Rotate した際の Cookie MaxAge。
	SessionMaxAge time.Duration
}

// viewers は 「閲覧」権限ロール集合 (general_user 以上)。
var viewers = []mw.Role{
	mw.RoleGeneralUser,
	mw.RoleViewer,
	mw.RoleLicenseManager,
	mw.RoleDepartmentSecurityAdmin,
	mw.RoleSystemAdmin,
}

// editors は 「編集」権限ロール集合 (license_manager 以上)。
var editors = []mw.Role{
	mw.RoleLicenseManager,
	mw.RoleDepartmentSecurityAdmin,
	mw.RoleSystemAdmin,
}

// departmentViewers は /departments 系の閲覧権限。要件書 §11 で
// 「viewer 以上」と規定されており、general_user は除外する
// (vendors / products の viewers より厳しい)。
var departmentViewers = []mw.Role{
	mw.RoleViewer,
	mw.RoleLicenseManager,
	mw.RoleDepartmentSecurityAdmin,
	mw.RoleSystemAdmin,
}

// RegisterRoutes は本パッケージのルートを r に登録する。
// 呼び出し側 (handler.NewRouter) で Session / Auth / CSRF middleware を
// r に適用済みの前提。
func RegisterRoutes(r chi.Router, deps Deps) {
	v := &vendorHandlers{db: deps.DB, logger: deps.Logger}
	p := &productHandlers{db: deps.DB, logger: deps.Logger}
	d := &departmentHandlers{db: deps.DB, logger: deps.Logger}
	u := &userHandlers{db: deps.DB, logger: deps.Logger}
	dev := &deviceHandlers{db: deps.DB, logger: deps.Logger}
	lic := &licenseHandlers{db: deps.DB, logger: deps.Logger}

	// /login / /logout は role 不問。Authenticator / SessionStore が注入
	// されている場合のみ登録する (テストで nil を渡したときに panic
	// しないように)。
	if deps.Authenticator != nil && deps.SessionStore != nil {
		a := &authHandlers{
			authenticator: deps.Authenticator,
			sessionStore:  deps.SessionStore,
			db:            deps.DB,
			cookieSecure:  deps.CookieSecure,
			sessionMaxAge: deps.SessionMaxAge,
			logger:        deps.Logger,
		}
		r.Get("/login", a.loginGet)
		r.Post("/login", a.loginPost)
		r.Post("/logout", a.logoutPost)
	}

	r.With(mw.RequireRole(viewers...)).Group(func(r chi.Router) {
		r.Get("/vendors", v.list)
		r.Get("/vendors/{id}", v.show)
		r.Get("/products", p.list)
		r.Get("/products/{id}", p.show)
	})
	r.With(mw.RequireRole(departmentViewers...)).Group(func(r chi.Router) {
		r.Get("/departments", d.list)
		r.Get("/departments/{id}", d.show)
		r.Get("/users", u.list)
		r.Get("/users/{id}", u.show)
		r.Get("/devices", dev.list)
		r.Get("/devices/{id}", dev.show)
		// /licenses の閲覧は要件書 §6.1 で「viewer 以上」(general_user 除外)。
		r.Get("/licenses", lic.list)
		r.Get("/licenses/{id}", lic.show)
	})
	r.With(mw.RequireRole(editors...)).Group(func(r chi.Router) {
		r.Get("/vendors/new", v.newForm)
		r.Post("/vendors", v.create)
		r.Get("/vendors/{id}/edit", v.editForm)
		r.Post("/vendors/{id}", v.update)
		r.Post("/vendors/{id}/delete", v.delete)
		r.Get("/products/new", p.newForm)
		r.Post("/products", p.create)
		r.Get("/products/{id}/edit", p.editForm)
		r.Post("/products/{id}", p.update)
		r.Post("/products/{id}/delete", p.delete)
		r.Post("/products/{id}/aliases", p.aliasCreate)
		r.Post("/products/{id}/aliases/{aid}/delete", p.aliasDelete)
		r.Get("/departments/new", d.newForm)
		r.Post("/departments", d.create)
		r.Get("/departments/{id}/edit", d.editForm)
		r.Post("/departments/{id}", d.update)
		r.Post("/departments/{id}/delete", d.delete)
		r.Post("/departments/{id}/restore", d.restore)
		r.Get("/users/new", u.newForm)
		r.Post("/users", u.create)
		r.Get("/users/{id}/edit", u.editForm)
		r.Post("/users/{id}", u.update)
		r.Post("/users/{id}/delete", u.delete)
		r.Post("/users/{id}/restore", u.restore)
		r.Get("/devices/new", dev.newForm)
		r.Post("/devices", dev.create)
		r.Get("/devices/{id}/edit", dev.editForm)
		r.Post("/devices/{id}", dev.update)
		r.Post("/devices/{id}/retire", dev.retire)
		r.Post("/devices/{id}/restore", dev.restore)
		// licenses に削除ルートは無い — 満了 = expires_at (論理削除規約)。
		r.Get("/licenses/new", lic.newForm)
		r.Post("/licenses", lic.create)
		r.Get("/licenses/{id}/edit", lic.editForm)
		r.Post("/licenses/{id}", lic.update)
	})
}
