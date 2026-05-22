package middleware

import (
	"context"
	"net/http"
)

// Role は要件書 §7.1 で定義された 5 つのロールに対応する。
type Role string

const (
	RoleSystemAdmin          Role = "system_admin"
	RoleDepartmentSecurityAd Role = "department_security_admin"
	RoleLicenseManager       Role = "license_manager"
	RoleViewer               Role = "viewer"
	RoleGeneralUser          Role = "general_user"
)

// roleKey は context への role 格納キー。
// revive の context-keys-type が string キーを禁止するため、
// 0 サイズ unexported 型で衝突回避と lint クリアを同時に達成する。
type roleKey struct{}

// validRoles は未知ヘッダ値を弾くための受理集合。
var validRoles = map[Role]struct{}{
	RoleSystemAdmin:          {},
	RoleDepartmentSecurityAd: {},
	RoleLicenseManager:       {},
	RoleViewer:               {},
	RoleGeneralUser:          {},
}

// DummyAuthMiddleware は HTTP ヘッダ X-User-Role から role を取り出し、
// context に詰めるダミー認可ミドルウェア (PR-A 用)。
//
//   - ヘッダ未設定 → RoleGeneralUser として扱う
//   - 既知の値 → そのまま context に格納
//   - 未知の値 → 403 Forbidden を返して next を呼ばない
//
// フェーズ 3 では本物のセッションから role を引いてくる middleware に
// 差し替える。handler 側は RoleFrom で取り出すため変更不要。
func DummyAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-User-Role")
		role := Role(raw)
		switch {
		case raw == "":
			role = RoleGeneralUser
		default:
			if _, ok := validRoles[role]; !ok {
				http.Error(w, "unknown role", http.StatusForbidden)
				return
			}
		}
		ctx := context.WithValue(r.Context(), roleKey{}, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RoleFrom は context から role を取り出す。未設定なら RoleGeneralUser。
func RoleFrom(ctx context.Context) Role {
	if v, ok := ctx.Value(roleKey{}).(Role); ok {
		return v
	}
	return RoleGeneralUser
}

// RequireRole は許可リストに含まれる role でのみ next を呼ぶハンドララッパ。
// 後続 PR で r.With(RequireRole(RoleSystemAdmin, RoleLicenseManager)).Get(...)
// の形で利用する。
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
