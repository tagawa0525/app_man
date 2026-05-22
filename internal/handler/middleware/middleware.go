// Package middleware は appmgr-server の HTTP ミドルウェア群を提供する。
//
// PR-A 時点では「ダミー認可」(X-User-Role ヘッダ → context への role 詰め込み)
// と「ダミー CSRF」(固定トークン検証) の 2 つを提供する。
// フェーズ 3 で本物のセッション・CSRF ジェネレータに差し替える際は、
// 各 middleware の検証ロジックだけを書き換え、handler 側のインタフェース
// (RoleFrom / RequireRole / DummyCSRFToken) は据え置く想定。
//
// chi/v5/middleware と名前空間が衝突するため、import 時はエイリアスを
// 付ける運用 (例: chimw "github.com/go-chi/chi/v5/middleware") とする。
package middleware
