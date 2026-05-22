package web

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

// roleCookieMaxAge は app_man_role Cookie の寿命。30 日 (秒数で表現)。
// dev で何度も切り替えても保持される。フェーズ 3 でセッション TTL に合わせる
// 必要が出れば再考する。
const roleCookieMaxAge = 30 * 24 * 60 * 60

// devHandlers は開発専用エンドポイントを束ねる。本物の認証が入る
// フェーズ 3 で削除 / act-as 機能への転用のどちらかを再判断する。
type devHandlers struct {
	logger *slog.Logger
}

// setRole は POST /__set_role を処理する。form 値 role を検証し、
// app_man_role Cookie に書き込んで Referer (同一オリジン) にリダイレクト。
// 認可はかけず、全 role が自由に切替可能。CSRF middleware が保護する。
func (h *devHandlers) setRole(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	role := middleware.Role(r.PostFormValue("role"))
	if !middleware.IsValidRole(role) {
		http.Error(w, "unknown role", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.RoleCookieName,
		Value:    string(role),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   roleCookieMaxAge,
	})
	http.Redirect(w, r, safeRedirect(r), http.StatusSeeOther)
}

// safeRedirect は Referer ヘッダが同一オリジンならその Path+Query を返し、
// そうでなければ "/" を返す。オープンリダイレクト回避のため Host 比較で
// 厳格に判定する。
func safeRedirect(r *http.Request) string {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return "/"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host != r.Host {
		return "/"
	}
	return u.RequestURI()
}
