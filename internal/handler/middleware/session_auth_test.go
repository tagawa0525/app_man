package middleware_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// seedUserWithRoles は app_users 1 行 + user_department_roles N 行を INSERT
// して app_user_id を返す。本 PR 範囲内の middleware テスト専用ヘルパ
// (handlertest.AuthenticatedAs と冗長だがレイヤ間依存を避けるため別実装)。
func seedUserWithRoles(t *testing.T, db *sql.DB, username string, roles ...middleware.Role) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx,
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, '', 'local')`,
		username)
	if err != nil {
		t.Fatalf("insert app_users: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	for _, r := range roles {
		_, err := db.ExecContext(ctx,
			`INSERT INTO user_department_roles (app_user_id, department_id, role) VALUES (?, NULL, ?)`,
			id, string(r))
		if err != nil {
			t.Fatalf("insert role %q: %v", r, err)
		}
	}
	return id
}

// newAuthChain は SessionMiddleware → AuthMiddleware (cfg) → next の
// 連結ハンドラを返す。テストの大半はこの helper を経由してリクエストを送る。
func newAuthChain(t *testing.T, cfg middleware.AuthConfig, next http.Handler) (http.Handler, session.Store) {
	t.Helper()
	store := session.NewSQLiteStore(cfg.DB)
	sess := middleware.SessionMiddleware(middleware.SessionConfig{
		Store:        store,
		SecureCookie: false,
		MaxAge:       time.Hour,
		Logger:       cfg.Logger,
	})
	auth := middleware.AuthMiddleware(cfg)
	return sess(auth(next)), store
}

// captureRoleAndPath は AuthMiddleware を通った後の role と path を観測する。
func captureRoleAndPath(captured *middleware.Role, capturedPath *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = middleware.RoleFrom(r.Context())
		*capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
}

// makeAuthenticatedRequest は store に session を作って Cookie を付けた
// リクエストを返す。
func makeAuthenticatedRequest(t *testing.T, store session.Store, appUserID int64, method, target string) *http.Request {
	t.Helper()
	id, err := session.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	tok, err := session.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	now := time.Now()
	if err := store.Create(context.Background(), session.Session{
		ID:         id,
		AppUserID:  &appUserID,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: id})
	return req
}

func defaultAuthConfig(t *testing.T) middleware.AuthConfig {
	t.Helper()
	return middleware.AuthConfig{
		DB:     handlertest.NewTestDB(t),
		Logger: slog.New(slog.DiscardHandler),
	}
}

func TestAuthMiddleware_PublicPath_PassesUnauthenticated(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	var role middleware.Role
	var path string
	chain, _ := newAuthChain(t, cfg, captureRoleAndPath(&role, &path))

	for _, p := range []string{"/login", "/login?next=/foo", "/static/app.css", "/healthz"} {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthMiddleware_Unauthenticated_RedirectsToLoginWithNext(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	chain, _ := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/products?q=adobe", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("Location = %q, want /login?next=...", loc)
	}
	// next にクエリも含めてエンコードされていること
	if !strings.Contains(loc, "products") || !strings.Contains(loc, "adobe") {
		t.Fatalf("Location = %q should contain the original path+query", loc)
	}
}

func TestAuthMiddleware_AuthenticatedSingleRole_SetsRoleInContext(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	appUserID := seedUserWithRoles(t, cfg.DB, "alice", middleware.RoleLicenseManager)

	var role middleware.Role
	var path string
	chain, store := newAuthChain(t, cfg, captureRoleAndPath(&role, &path))

	req := makeAuthenticatedRequest(t, store, appUserID, http.MethodGet, "/products")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if role != middleware.RoleLicenseManager {
		t.Fatalf("role = %q, want %q", role, middleware.RoleLicenseManager)
	}
}

func TestAuthMiddleware_AuthenticatedMultipleRoles_PicksHighest(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	// AllRoles() 順: system_admin > department_security_admin > license_manager > viewer > general_user
	// シャッフルした順で INSERT して、最高権限が選ばれることを確認する
	appUserID := seedUserWithRoles(t, cfg.DB, "bob",
		middleware.RoleGeneralUser,
		middleware.RoleDepartmentSecurityAdmin,
		middleware.RoleViewer)

	var role middleware.Role
	var path string
	chain, store := newAuthChain(t, cfg, captureRoleAndPath(&role, &path))

	req := makeAuthenticatedRequest(t, store, appUserID, http.MethodGet, "/products")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if role != middleware.RoleDepartmentSecurityAdmin {
		t.Fatalf("role = %q, want %q (highest of 3)", role, middleware.RoleDepartmentSecurityAdmin)
	}
}

func TestAuthMiddleware_AuthenticatedNoRole_Returns403(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	appUserID := seedUserWithRoles(t, cfg.DB, "charlie") // role 0 件

	chain, store := newAuthChain(t, cfg, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next should not be called for user with 0 active roles")
	}))

	req := makeAuthenticatedRequest(t, store, appUserID, http.MethodGet, "/products")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAuthMiddleware_RevokedRole_Ignored(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	appUserID := seedUserWithRoles(t, cfg.DB, "dave", middleware.RoleLicenseManager)
	// revoke 後に active role 0 件 → 403
	if _, err := cfg.DB.Exec(`UPDATE user_department_roles SET revoked_at = ? WHERE app_user_id = ?`,
		time.Now(), appUserID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	chain, store := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := makeAuthenticatedRequest(t, store, appUserID, http.MethodGet, "/products")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (revoked = no active role)", rec.Code)
	}
}

func TestAuthMiddleware_CustomLoginURL(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)
	cfg.LoginURL = "/custom-login"

	chain, _ := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/products", nil))
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/custom-login?next=") {
		t.Fatalf("Location = %q, want /custom-login?next=...", loc)
	}
}

func TestAuthMiddleware_CustomPublicPrefix(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)
	cfg.PublicPathPrefixes = []string{"/api/public/"} // デフォルトを上書き

	chain, _ := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// /api/public/ping は素通り
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/public/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/public/ping: status = %d, want 200", rec.Code)
	}

	// デフォルトに含まれていた /healthz は今度は public ではない (上書きされた)
	rec = httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("/healthz: status = %d, want 303 (overridden public list excludes it)", rec.Code)
	}
}

func TestAuthMiddleware_PublicPath_NotPrefixOnSingleEntry(t *testing.T) {
	t.Parallel()
	cfg := defaultAuthConfig(t)

	chain, _ := newAuthChain(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 末尾に "/" を持たない "/login" は完全一致のみ。"/loginxxx" は素通りしない
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/loginxxx", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("/loginxxx: status = %d, want 303 (must NOT be treated as public)", rec.Code)
	}
}

func TestAuthMiddleware_Panics_WhenDBNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when DB is nil")
		}
	}()
	middleware.AuthMiddleware(middleware.AuthConfig{DB: nil})
}
