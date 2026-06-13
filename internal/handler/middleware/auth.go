package middleware

import (
	"context"
	"net/http"
)

// Role は要件書 §7.1 で定義された 5 つのロールに対応する。
type Role string

const (
	RoleSystemAdmin             Role = "system_admin"
	RoleDepartmentSecurityAdmin Role = "department_security_admin"
	RoleLicenseManager          Role = "license_manager"
	RoleViewer                  Role = "viewer"
	RoleGeneralUser             Role = "general_user"
)

// roleKey は context への role 格納キー。
// revive の context-keys-type が string キーを禁止するため、
// 0 サイズ unexported 型で衝突回避と lint クリアを同時に達成する。
type roleKey struct{}

// validRoles は不明な role 文字列を弾くための受理集合。
// IsValidRole / pickHighestRole (session_auth.go) から参照される。
var validRoles = map[Role]struct{}{
	RoleSystemAdmin:             {},
	RoleDepartmentSecurityAdmin: {},
	RoleLicenseManager:          {},
	RoleViewer:                  {},
	RoleGeneralUser:             {},
}

// IsValidRole は要件書 §7.1 で定義された 5 ロールのいずれかか判定する。
// CLI (appmgr-create-app-user) の form 値検証と AuthMiddleware の
// user_department_roles 行から不正値を捨てる用途で使う。
func IsValidRole(role Role) bool {
	_, ok := validRoles[role]
	return ok
}

// AllRoles は要件書 §7.1 で定義された 5 ロールの一覧を順序付きで返す。
// IsValidRole と同じ集合 (validRoles) と一致するが、ユーザに「許可ロール」を
// 提示する用途 (CLI のエラーメッセージ) や、最高権限選択
// (AuthMiddleware の pickHighestRole) で順序を保ちたいときに使う。
// 順序は仕様書 §7.1 の記載順 (権限が広い順)。
func AllRoles() []Role {
	return []Role{
		RoleSystemAdmin,
		RoleDepartmentSecurityAdmin,
		RoleLicenseManager,
		RoleViewer,
		RoleGeneralUser,
	}
}

// RoleFrom は context から role を取り出す。未設定なら RoleGeneralUser。
//
// AuthMiddleware は認証済リクエスト (= 非公開パス) でのみ role を context に
// 詰めるため、公開パス (/login / /logout / /healthz / /static/*) のハンドラや
// AuthMiddleware を経由しないテストでは general_user フォールバックが返る。
// 公開パスのテンプレが Role を見ない設計なら問題にならない。
func RoleFrom(ctx context.Context) Role {
	if v, ok := ctx.Value(roleKey{}).(Role); ok {
		return v
	}
	return RoleGeneralUser
}

// WithRole は context に role を詰めて返す。AuthMiddleware の経路を通さず
// 直接 RequireRole 等を呼ぶテストや、ハンドラ単体テストで使う。
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// RequireRole は許可リストに含まれる role でのみ next を呼ぶハンドララッパ。
// 例: r.With(RequireRole(RoleSystemAdmin, RoleLicenseManager)).Get(...)
func RequireRole(allowed ...Role) func(http.Handler) http.Handler {
	allowedSet := make(map[Role]struct{}, len(allowed))
	for _, role := range allowed {
		allowedSet[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowedSet[RoleFrom(r.Context())]; !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
