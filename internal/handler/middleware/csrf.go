package middleware

import "net/http"

// DummyCSRFToken はフェーズ 3 で session-bound 値に置き換える前提の固定値。
// middleware の検証ロジック、template の hidden input、handlertest の
// POST helper の 3 箇所から参照されるため、ここに 1 箇所で定義する。
//
// 検証ロジックは固定値比較で済ませているが、フェーズ 3 で session に紐づく
// 値を発行・検証する形に書き換える際も、handler / template 側のインタフェース
// (X-CSRF-Token ヘッダ or _csrf form フィールド) は据え置く想定。
const DummyCSRFToken = "dummy-csrf-token"

// CSRFMiddleware は GET / HEAD / OPTIONS を素通りし、それ以外は
// X-CSRF-Token ヘッダ or form 値 `_csrf` が DummyCSRFToken と一致する
// ときのみ next を呼ぶ。一致しなければ 403 Forbidden を返す。
//
// 仕様書 §8.3「GET 以外のリクエストには CSRF トークンを必須」に対応する
// ダミー実装。フェーズ 3 で本物のセッション + トークン生成に差し替える。
func CSRFMiddleware(_ http.Handler) http.Handler {
	// stub: 次コミットで実装
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
}
