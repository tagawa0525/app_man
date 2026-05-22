package middleware

import (
	"crypto/subtle"
	"net/http"
)

// DummyCSRFToken はフェーズ 3 で session-bound 値に置き換える前提の固定値。
// middleware の検証ロジック、template の hidden input、handlertest の
// POST helper の 3 箇所から参照されるため、ここに 1 箇所で定義する。
//
// 検証ロジックは固定値比較で済ませているが、フェーズ 3 で session に紐づく
// 値を発行・検証する形に書き換える際も、handler / template 側のインタフェース
// (X-CSRF-Token ヘッダ or _csrf form フィールド) は据え置く想定。
const DummyCSRFToken = "dummy-csrf-token"

// safeMethods は CSRF 検証を素通りさせる HTTP メソッド集合。
// RFC 9110 で「安全」と定義されるメソッドに沿う。
var safeMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// CSRFMiddleware は GET / HEAD / OPTIONS を素通りし、それ以外は
// X-CSRF-Token ヘッダ or form 値 `_csrf` が DummyCSRFToken と一致する
// ときのみ next を呼ぶ。一致しなければ 403 Forbidden を返す。
//
// 仕様書 §8.3「GET 以外のリクエストには CSRF トークンを必須」に対応する
// ダミー実装。フェーズ 3 で本物のセッション + トークン生成に差し替える。
func CSRFMiddleware(next http.Handler) http.Handler {
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

		if subtle.ConstantTimeCompare([]byte(token), []byte(DummyCSRFToken)) != 1 {
			http.Error(w, "csrf token mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
