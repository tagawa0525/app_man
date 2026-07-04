package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// CSRFTokenFrom は SessionFrom(ctx) から CSRF token を取り出す。
// session が無い / ephemeral (CSRFToken == "") の場合は空文字を返す。
// templ 側で同じ token を form の hidden / meta tag に埋め、
// CSRFMiddleware が POST 等で検証する。
func CSRFTokenFrom(ctx context.Context) string {
	if s := SessionFrom(ctx); s != nil {
		return s.CSRFToken
	}
	return ""
}

// safeMethods は CSRF 検証を素通りさせる HTTP メソッド集合。
// RFC 9110 で「安全」と定義されるメソッドに沿う。
var safeMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// CSRFMiddleware は GET / HEAD / OPTIONS を素通りし、それ以外は
// X-CSRF-Token ヘッダ or form 値 `_csrf` が SessionFrom(ctx).CSRFToken と
// 一致するときのみ next を呼ぶ。一致しなければ 403 Forbidden を返す。
//
// 仕様書 §8.3「サーバ側はセッションごとに発行したトークンと突合」に対応。
// SessionMiddleware の後段で動く前提 (順序が逆だと SessionFrom が nil で
// 常に 403 になる)。
//
// session が ephemeral (CSRFToken == "") の場合は受理トークンが空なので
// 必ず 403 になる。空文字一致を偶発的に通さないためのガード。
//
// multipartMaxBytes は multipart form の hidden _csrf を読むために body を
// パースする際の上限。CSRF 検証前 (= トークン不一致の攻撃リクエストでも)
// にパースが走るため、上限なしだと巨大 body を一時ファイルへ書き出して
// しまう (DoS 入口)。router 組立側で file_store.upload_max_bytes +
// フォームフィールド分の余裕を渡す。超過は 400 で拒否し next を呼ばない。
func CSRFMiddleware(multipartMaxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, safe := safeMethods[r.Method]; safe {
				next.ServeHTTP(w, r)
				return
			}

			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				// form 値の参照は ParseForm が必要。Content-Type が
				// application/x-www-form-urlencoded のときだけ意味がある。
				if err := r.ParseForm(); err == nil {
					token = r.PostFormValue("_csrf")
				}
			}
			if token == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				// multipart form (証書アップロード等) の hidden _csrf は
				// ParseForm では読めないため multipart をパースする。ここで
				// パース済みになった body は後段ハンドラの ParseMultipartForm
				// ではキャッシュが返る。maxMemory 超過分は一時ファイルに
				// 落ちるため、書き出し量を MaxBytesReader で頭打ちにする
				// (ファイル本体の厳密な上限は filestore 層との二重防御)。
				r.Body = http.MaxBytesReader(w, r.Body, multipartMaxBytes)
				if err := r.ParseMultipartForm(32 << 20); err != nil {
					var maxErr *http.MaxBytesError
					if errors.As(err, &maxErr) {
						http.Error(w, "request body too large", http.StatusBadRequest)
						return
					}
					// その他のパース失敗は token 無しのまま下の 403 に落とす。
				} else {
					token = r.PostFormValue("_csrf")
				}
			}

			expected := CSRFTokenFrom(r.Context())
			if expected == "" || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
				http.Error(w, "csrf token mismatch", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
