package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// defaultLoginURL は AuthConfig.LoginURL のデフォルト値。
const defaultLoginURL = "/login"

// AuthConfig は AuthMiddleware の依存。
//
//   - DB: user_department_roles 引き用 (必須)
//   - Logger: nil なら slog.Default
//   - LoginURL: 未認証時のリダイレクト先 (default "/login")
//   - PublicPathPrefixes: 認証不要パスの prefix リスト
//     (default ["/login", "/static/", "/healthz"])
type AuthConfig struct {
	DB                 *sql.DB
	Logger             *slog.Logger
	LoginURL           string
	PublicPathPrefixes []string
}

// AuthMiddleware は SessionMiddleware の後段で動く認可ミドルウェア。
//
//   - 公開パスは素通り
//   - SessionFrom(ctx) == nil は router 組立ミスとして 500 + error ログ
//   - session.AppUserID == nil は /login?next=<original> に 303 redirect
//   - session.AppUserID != nil は user_department_roles を引いて最高権限 role を
//     context に詰める。0 件なら 403
//
// 仕様書 §7.2「セッションからログインユーザを特定 → user_department_roles を
// 引き保有ロール・部署を取得」に対応する MVP 実装。部署別認可は別 PR。
func AuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	if cfg.DB == nil {
		panic("middleware.AuthMiddleware: DB is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.LoginURL == "" {
		cfg.LoginURL = defaultLoginURL
	}
	if cfg.PublicPathPrefixes == nil {
		// /logout は role が無い (revoked / disabled) ユーザでも到達できるよう
		// 認可チェックの外に置く。ハンドラは SessionFrom() で session を読み
		// store.Delete(ID) するだけなので役割は不要。
		cfg.PublicPathPrefixes = []string{"/static/", "/healthz", "/logout"}
	}
	// LoginURL の path 部を必ず公開リストに含める。LoginURL を /auth 等に
	// 変更したときに、未認証アクセスがログインページ自体に redirect されて
	// 無限ループになるのを防ぐ。
	if loginPath := extractPath(cfg.LoginURL); loginPath != "" && !isPublicPath(loginPath, cfg.PublicPathPrefixes) {
		cfg.PublicPathPrefixes = append(cfg.PublicPathPrefixes, loginPath)
	}

	q := repository.New(cfg.DB)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicPath(r.URL.Path, cfg.PublicPathPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			sess := SessionFrom(r.Context())
			if sess == nil {
				cfg.Logger.ErrorContext(r.Context(),
					"AuthMiddleware: no session in context (SessionMiddleware not in chain?)")
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			if sess.AppUserID == nil {
				cfg.Logger.InfoContext(r.Context(), "redirect to login", "path", r.URL.Path)
				http.Redirect(w, r, cfg.LoginURL+"?next="+url.QueryEscape(originalURI(r)), http.StatusSeeOther)
				return
			}

			rows, err := q.ListActiveRolesForAppUser(r.Context(), *sess.AppUserID)
			if err != nil {
				cfg.Logger.ErrorContext(r.Context(), "ListActiveRolesForAppUser", "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			role := pickHighestRole(rows)
			if role == "" {
				cfg.Logger.WarnContext(r.Context(), "user has no active role",
					"app_user_id", *sess.AppUserID)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithRole(r.Context(), role)))
		})
	}
}

// isPublicPath は path がいずれかの公開エントリにマッチするかを返す。
//
// マッチルール:
//   - エントリが末尾 "/" で終わる → ディレクトリ用途の prefix マッチ
//     (例: "/static/" は "/static/app.css" にマッチ、"/static" 単体には不一致)
//   - エントリが末尾 "/" 以外 → 完全一致 (クエリ無しの URL.Path)
//     (例: "/login" は "/login" にマッチするが "/loginxxx" には不一致)
//
// クエリは URL.Path に含まれないので別途考慮不要。
func isPublicPath(path string, entries []string) bool {
	for _, e := range entries {
		if strings.HasSuffix(e, "/") {
			if strings.HasPrefix(path, e) {
				return true
			}
		} else if path == e {
			return true
		}
	}
	return false
}

// originalURI はリダイレクト先の next クエリに渡す URI を組み立てる。
// path + raw query を併せる (フラグメントは server に届かないので無視)。
func originalURI(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

// extractPath は LoginURL の path 部を返す。url.Parse 失敗時や Path が空なら
// "" を返す (呼び出し側で「補完しない」を選ぶ)。
func extractPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Path
}

// pickHighestRole は rows の中で AllRoles() 順最初に出現する Role を返す。
// rows に既知 Role が含まれていなければ "" を返す (= 403 経路へ)。
func pickHighestRole(rows []repository.ListActiveRolesForAppUserRow) Role {
	have := make(map[Role]struct{}, len(rows))
	for _, row := range rows {
		r := Role(row.Role)
		if IsValidRole(r) {
			have[r] = struct{}{}
		}
	}
	for _, candidate := range AllRoles() {
		if _, ok := have[candidate]; ok {
			return candidate
		}
	}
	return ""
}
