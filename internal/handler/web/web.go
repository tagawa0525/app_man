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

	"github.com/go-chi/chi/v5"

	mw "github.com/tagawa0525/app_man/internal/handler/middleware"
)

// Deps は web ハンドラが必要とする外部依存。internal/handler の Deps と
// 同型だが、循環 import を避けるため web パッケージ独自に定義する。
type Deps struct {
	Logger *slog.Logger
	DB     *sql.DB
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

// RegisterRoutes は本パッケージのルートを r に登録する。
// 呼び出し側 (handler.NewRouter) で DummyAuth / CSRF middleware を r に
// 適用済みの前提。
func RegisterRoutes(_ chi.Router, _ Deps) {
	// 実装は後続コミットで追加する (vendors → products → alias)。
}
