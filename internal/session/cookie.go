package session

import (
	"net/http"
	"time"
)

// CookieName は session ID を保持する Cookie 名。
// 役割 (role 切替) Cookie の RoleCookieName と prefix を揃えて、
// dev 用に手動操作するときの取り回しを良くしている。
const CookieName = "app_man_session"

// SetCookie は w にセッション Cookie を Set-Cookie で書き出す。
// HttpOnly / SameSite=Lax は仕様書 §7.3 / §8.3 で必須。
// secure は本番 (HTTPS) で true、dev (HTTP) で false にする想定で
// 呼び出し側 (SessionMiddleware) が server.cookie_secure 設定値を渡す。
func SetCookie(w http.ResponseWriter, id string, maxAge time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie はブラウザに Cookie を削除させる Set-Cookie を書き出す
// (Value 空 + MaxAge<0)。Cookie 不整合検出時とログアウト時に呼ぶ。
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadCookie はリクエストから session Cookie 値を取り出す。
// Cookie が無いか読み取りエラーなら空文字を返す。
func ReadCookie(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
