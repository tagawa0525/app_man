// Package middleware は appmgr-server の HTTP ミドルウェア群を提供する。
//
// 提供する middleware:
//
//   - SessionMiddleware: Cookie ベースのセッション読み込み / 新規発行
//   - AuthMiddleware: session.AppUserID から user_department_roles を引き、
//     最高権限 role を context に詰める (未認証は /login にリダイレクト)
//   - CSRFMiddleware: GET 以外のリクエストに CSRF トークン検証を強制
//     (X-CSRF-Token ヘッダ or _csrf form 値が SessionFrom(ctx).CSRFToken と
//     一致することを確認)
//   - RequireRole: 許可ロールリストに含まれる role のみ next を呼ぶ
//
// chi/v5/middleware と名前空間が衝突するため、import 時はエイリアスを
// 付ける運用 (例: chimw "github.com/go-chi/chi/v5/middleware") とする。
package middleware
