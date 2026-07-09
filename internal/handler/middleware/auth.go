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

// RoleGrant は user_department_roles の 1 行 (role, department_id) に対応
// する。DepartmentID が nil の行は全社スコープ (どの部署に対しても有効)。
type RoleGrant struct {
	Role         Role
	DepartmentID *int64
}

// roleGrantsKey は context への []RoleGrant 格納キー (roleKey と同流儀)。
type roleGrantsKey struct{}

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

// RoleGrantsFrom は context から全ロール行を取り出す。未設定なら nil
// (= どの部署に対しても HasDepartmentRole は false)。AuthMiddleware が
// 認証済リクエストで必ず詰めるため、nil になるのは AuthMiddleware を
// 経由しない経路のみ。
func RoleGrantsFrom(ctx context.Context) []RoleGrant {
	if v, ok := ctx.Value(roleGrantsKey{}).([]RoleGrant); ok {
		return v
	}
	return nil
}

// WithRoleGrants は context に全ロール行を詰めて返す。AuthMiddleware と、
// その経路を通さず HasDepartmentRole を直接検証するテストが使う。
func WithRoleGrants(ctx context.Context, grants []RoleGrant) context.Context {
	return context.WithValue(ctx, roleGrantsKey{}, grants)
}

// roleRank は AllRoles() 順の位置 (小さいほど権限が広い) を返す。
// 未知 role は最下位 + 1 (どの minRole も満たさない)。
func roleRank(role Role) int {
	for i, r := range AllRoles() {
		if r == role {
			return i
		}
	}
	return len(AllRoles())
}

// HasDepartmentRole は「deptID の部署に対して minRole 以上の権限を持つか」
// (仕様 §7.2 の部署スコープ判定) を返す。
//
//   - system_admin のロール行があれば常に true (全社スコープのロール)
//   - それ以外は minRole 以上のロール行 (「以上」は AllRoles() の記載順 =
//     RequireRole の許可集合と同じ包含解釈) のうち、DepartmentID が deptID
//     に一致するか NULL (全社スコープ行) のものがあれば true
//
// ルート単位の RequireRole (ロール階層) を通過した後の第 2 層として、
// 書込み系ハンドラが対象データの所属部署を渡して呼ぶ。
func HasDepartmentRole(ctx context.Context, minRole Role, deptID int64) bool {
	minRank := roleRank(minRole)
	for _, g := range RoleGrantsFrom(ctx) {
		if g.Role == RoleSystemAdmin {
			return true
		}
		if roleRank(g.Role) > minRank {
			continue
		}
		if g.DepartmentID == nil || *g.DepartmentID == deptID {
			return true
		}
	}
	return false
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
