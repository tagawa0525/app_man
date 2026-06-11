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

// RoleCookieName は dev 向けロール切替で利用する Cookie 名。
// 本物の認証が入るフェーズ 3 で扱いを再判断する。
const RoleCookieName = "app_man_role"

// validRoles は未知ヘッダ値を弾くための受理集合。
var validRoles = map[Role]struct{}{
	RoleSystemAdmin:             {},
	RoleDepartmentSecurityAdmin: {},
	RoleLicenseManager:          {},
	RoleViewer:                  {},
	RoleGeneralUser:             {},
}

// IsValidRole は要件書 §7.1 で定義された 5 ロールのいずれかか判定する。
// dev 用 /__set_role ハンドラから外部入力 (form 値) を検証する用途。
func IsValidRole(role Role) bool {
	_, ok := validRoles[role]
	return ok
}

// AllRoles は要件書 §7.1 で定義された 5 ロールの一覧を順序付きで返す。
// IsValidRole と同じ集合 (validRoles) と一致するが、ユーザに「許可ロール」を
// 提示する用途 (CLI のエラーメッセージ、UI のセレクトボックス等) で
// 順序を保ちたいときに使う。順序は仕様書 §7.1 の記載順 (権限が広い順)。
func AllRoles() []Role {
	return []Role{
		RoleSystemAdmin,
		RoleDepartmentSecurityAdmin,
		RoleLicenseManager,
		RoleViewer,
		RoleGeneralUser,
	}
}

// DummyAuthMiddleware は HTTP ヘッダ X-User-Role から role を取り出し、
// context に詰めるダミー認可ミドルウェア (PR-A 用)。
//
//   - ヘッダ未設定 → Cookie RoleCookieName をフォールバックとして見る
//   - Cookie もないか不正値 → RoleGeneralUser (不正値の Cookie は削除)
//   - 既知の値 → そのまま context に格納
//   - ヘッダの未知値 → 403 Forbidden (Cookie の場合は寛容に general_user)
//
// フェーズ 3 では本物のセッションから role を引いてくる middleware に
// 差し替える。handler 側は RoleFrom で取り出すため変更不要。
func DummyAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-User-Role")
		if raw == "" {
			raw = roleFromCookie(w, r)
		}
		role := Role(raw)
		switch raw {
		case "":
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

// roleFromCookie は app_man_role Cookie の値を返す。
// Cookie が無いか不正値ならば空文字を返し、不正値の場合は Cookie を削除する
// Set-Cookie をレスポンスに付与する (整合性保護)。
func roleFromCookie(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(RoleCookieName)
	if err != nil {
		return ""
	}
	if _, ok := validRoles[Role(c.Value)]; !ok {
		http.SetCookie(w, &http.Cookie{
			Name:   RoleCookieName,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		return ""
	}
	return c.Value
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
