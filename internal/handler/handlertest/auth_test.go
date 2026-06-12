package handlertest_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// queryRoles は user_department_roles をテストから直接確認するヘルパ。
func queryRoles(t *testing.T, db *sql.DB, appUserID int64) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT role FROM user_department_roles WHERE app_user_id = ? AND revoked_at IS NULL`,
		appUserID)
	if err != nil {
		t.Fatalf("query roles: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestAuthenticatedAs_CreatesAppUserRoleAndSession(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(db)

	cookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleSystemAdmin)
	if cookie == nil {
		t.Fatal("AuthenticatedAs returned nil cookie")
	}
	if cookie.Name != session.CookieName {
		t.Errorf("cookie Name = %q, want %q", cookie.Name, session.CookieName)
	}

	// session が DB にあって AppUserID が埋まっている
	sess, err := store.GetByID(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sess.AppUserID == nil {
		t.Fatal("session.AppUserID is nil")
	}

	// その app_user が指定 role を持っている
	roles := queryRoles(t, db, *sess.AppUserID)
	if len(roles) != 1 || roles[0] != string(middleware.RoleSystemAdmin) {
		t.Fatalf("roles = %v, want [%q]", roles, middleware.RoleSystemAdmin)
	}
}

func TestAuthenticatedAs_DistinctUsersForEachCall(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(db)

	a := handlertest.AuthenticatedAs(t, db, store, middleware.RoleViewer)
	b := handlertest.AuthenticatedAs(t, db, store, middleware.RoleViewer)

	if a.Value == b.Value {
		t.Fatal("AuthenticatedAs returned the same session cookie twice")
	}

	sessA, _ := store.GetByID(context.Background(), a.Value)
	sessB, _ := store.GetByID(context.Background(), b.Value)
	if sessA.AppUserID == nil || sessB.AppUserID == nil {
		t.Fatal("both sessions should have AppUserID")
	}
	if *sessA.AppUserID == *sessB.AppUserID {
		t.Fatal("AppUserID should differ between two AuthenticatedAs calls")
	}
}

// 実際に SessionMiddleware → AuthMiddleware → role 抽出を通せることを確認する
// 統合的なテスト。Plan の受け入れ基準「戻り値 Cookie で実際にチェーンを通せる」に対応。
func TestAuthenticatedAs_WorksWithSessionAndAuthMiddleware(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(db)

	cookie := handlertest.AuthenticatedAs(t, db, store, middleware.RoleLicenseManager)

	var gotRole middleware.Role
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole = middleware.RoleFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	chain := middleware.SessionMiddleware(middleware.SessionConfig{
		Store:  store,
		MaxAge: 3600,
	})(middleware.AuthMiddleware(middleware.AuthConfig{DB: db})(inner))

	req := httptest.NewRequest(http.MethodGet, "/products", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if gotRole != middleware.RoleLicenseManager {
		t.Fatalf("role = %q, want %q", gotRole, middleware.RoleLicenseManager)
	}
}
