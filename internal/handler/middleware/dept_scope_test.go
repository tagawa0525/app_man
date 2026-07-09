package middleware_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

// dept_scope_test.go は部署スコープ認可の基盤 (仕様 §7.2 / Plan
// department-scope-authz.md) をテストする:
//
//   - AuthMiddleware が全ロール行 (role, department_id) を
//     RoleGrantsFrom(ctx) で公開する (既存 RoleFrom = 最高ロールは互換維持)
//   - HasDepartmentRole(ctx, minRole, deptID) が「system_admin は常に true /
//     minRole 以上のロール行で department_id 一致 or NULL (全社スコープ行)」
//     を判定する

// seedDepartment は departments に 1 行 INSERT して id を返す
// (user_department_roles.department_id の FK 先)。
func seedDepartment(t *testing.T, db *sql.DB, code, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO departments (code, name) VALUES (?, ?)`, code, name)
	if err != nil {
		t.Fatalf("insert departments: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

// seedRoleRow は user_department_roles に (role, department_id) を 1 行
// INSERT する。deptID nil は全社スコープ行。
func seedRoleRow(t *testing.T, db *sql.DB, appUserID int64, role string, deptID *int64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO user_department_roles (app_user_id, department_id, role) VALUES (?, ?, ?)`,
		appUserID, deptID, role); err != nil {
		t.Fatalf("insert role row (%s, %v): %v", role, deptID, err)
	}
}

func deptPtr(v int64) *int64 { return &v }

func TestHasDepartmentRole(t *testing.T) {
	t.Parallel()

	grant := func(role middleware.Role, deptID *int64) middleware.RoleGrant {
		return middleware.RoleGrant{Role: role, DepartmentID: deptID}
	}

	cases := []struct {
		name    string
		grants  []middleware.RoleGrant
		minRole middleware.Role
		deptID  int64
		want    bool
	}{
		{
			name:    "自部署の license_manager 行は true",
			grants:  []middleware.RoleGrant{grant(middleware.RoleLicenseManager, deptPtr(1))},
			minRole: middleware.RoleLicenseManager,
			deptID:  1,
			want:    true,
		},
		{
			name:    "他部署の license_manager 行のみは false",
			grants:  []middleware.RoleGrant{grant(middleware.RoleLicenseManager, deptPtr(1))},
			minRole: middleware.RoleLicenseManager,
			deptID:  2,
			want:    false,
		},
		{
			name:    "全社スコープ行 (department_id NULL) は任意部署で true",
			grants:  []middleware.RoleGrant{grant(middleware.RoleLicenseManager, nil)},
			minRole: middleware.RoleLicenseManager,
			deptID:  5,
			want:    true,
		},
		{
			name:    "system_admin 行があれば部署不一致でも true",
			grants:  []middleware.RoleGrant{grant(middleware.RoleSystemAdmin, deptPtr(1))},
			minRole: middleware.RoleLicenseManager,
			deptID:  2,
			want:    true,
		},
		{
			name:    "下位ロール (viewer) のみでは部署一致でも false",
			grants:  []middleware.RoleGrant{grant(middleware.RoleViewer, deptPtr(1))},
			minRole: middleware.RoleLicenseManager,
			deptID:  1,
			want:    false,
		},
		{
			name: "上位ロール (dept_security_admin) は license_manager 以上として true",
			grants: []middleware.RoleGrant{
				grant(middleware.RoleDepartmentSecurityAdmin, deptPtr(1)),
			},
			minRole: middleware.RoleLicenseManager,
			deptID:  1,
			want:    true,
		},
		{
			name:    "license_manager 行は dept_security_admin 要件を満たさない",
			grants:  []middleware.RoleGrant{grant(middleware.RoleLicenseManager, deptPtr(1))},
			minRole: middleware.RoleDepartmentSecurityAdmin,
			deptID:  1,
			want:    false,
		},
		{
			name: "他部署行と自部署行の混在は自部署行で true",
			grants: []middleware.RoleGrant{
				grant(middleware.RoleLicenseManager, deptPtr(2)),
				grant(middleware.RoleLicenseManager, deptPtr(1)),
			},
			minRole: middleware.RoleLicenseManager,
			deptID:  1,
			want:    true,
		},
		{
			name:    "grants が空なら false",
			grants:  []middleware.RoleGrant{},
			minRole: middleware.RoleLicenseManager,
			deptID:  1,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := middleware.WithRoleGrants(context.Background(), tc.grants)
			if got := middleware.HasDepartmentRole(ctx, tc.minRole, tc.deptID); got != tc.want {
				t.Fatalf("HasDepartmentRole(%s, %d) = %v, want %v (grants: %+v)",
					tc.minRole, tc.deptID, got, tc.want, tc.grants)
			}
		})
	}
}

// grants 未設定の context (AuthMiddleware を通らない経路) は常に拒否。
func TestHasDepartmentRole_NoGrantsInContext(t *testing.T) {
	t.Parallel()
	if middleware.HasDepartmentRole(context.Background(), middleware.RoleLicenseManager, 1) {
		t.Fatal("HasDepartmentRole on plain context = true, want false")
	}
}

func TestRoleGrantsFrom_DefaultEmpty(t *testing.T) {
	t.Parallel()
	if got := middleware.RoleGrantsFrom(context.Background()); len(got) != 0 {
		t.Fatalf("RoleGrantsFrom(plain ctx) = %+v, want empty", got)
	}
}

// AuthMiddleware は既に取得している全ロール行を追加クエリなしで context に
// 公開する。既存の RoleFrom (最高ロール) は互換維持。
func TestAuthMiddleware_ExposesRoleGrants(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	deptA := seedDepartment(t, cfg.DB, "DEPTA", "部署A")
	appUserID := seedUserWithRoles(t, cfg.DB, "grants_user") // role 行はこの後直接 INSERT
	seedRoleRow(t, cfg.DB, appUserID, string(middleware.RoleLicenseManager), &deptA)
	seedRoleRow(t, cfg.DB, appUserID, string(middleware.RoleViewer), nil)
	// 不明 role 行は pickHighestRole と同様に grants からも除外される。
	seedRoleRow(t, cfg.DB, appUserID, "bogus_role", nil)

	var grants []middleware.RoleGrant
	var role middleware.Role
	chain, store := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grants = middleware.RoleGrantsFrom(r.Context())
		role = middleware.RoleFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := makeAuthenticatedRequest(t, store, appUserID, http.MethodGet, "/products")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if role != middleware.RoleLicenseManager {
		t.Errorf("RoleFrom = %q, want %q (互換維持)", role, middleware.RoleLicenseManager)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %+v, want 2 rows (bogus_role excluded)", grants)
	}
	var haveDeptLM, haveGlobalViewer bool
	for _, g := range grants {
		switch {
		case g.Role == middleware.RoleLicenseManager && g.DepartmentID != nil && *g.DepartmentID == deptA:
			haveDeptLM = true
		case g.Role == middleware.RoleViewer && g.DepartmentID == nil:
			haveGlobalViewer = true
		}
	}
	if !haveDeptLM || !haveGlobalViewer {
		t.Errorf("grants = %+v, want license_manager@deptA and viewer@NULL", grants)
	}
}
