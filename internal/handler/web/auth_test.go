package web_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/session"
)

// newAuthRouter は SessionMiddleware を組み込んだ chi.Router を返す。
// /login / /logout のフローは session 必須なので vendors 系より配線が重い。
func newAuthRouter(t *testing.T) (http.Handler, session.Store, *sql.DB) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(sqlDB)

	r := chi.NewRouter()
	r.Use(middleware.SessionMiddleware(middleware.SessionConfig{
		Store:        store,
		SecureCookie: false,
		MaxAge:       time.Hour,
		Logger:       slog.New(slog.DiscardHandler),
	}))
	r.Use(middleware.AuthMiddleware(middleware.AuthConfig{
		DB:     sqlDB,
		Logger: slog.New(slog.DiscardHandler),
	}))
	r.Use(middleware.CSRFMiddleware)
	web.RegisterRoutes(r, web.Deps{
		Logger:        slog.New(slog.DiscardHandler),
		DB:            sqlDB,
		Authenticator: auth.NewLocalAuthenticator(sqlDB),
		SessionStore:  store,
		CookieSecure:  false,
		SessionMaxAge: time.Hour,
	})
	return r, store, sqlDB
}

// seedLocalAdmin は app_users に system_admin 想定のローカルユーザを INSERT する。
func seedLocalAdmin(t *testing.T, db *sql.DB, username, password string) int64 {
	t.Helper()
	hash, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("auth.Hash: %v", err)
	}
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, ?, 'local')`,
		username, hash)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

func TestLogin_Get_ReturnsForm(t *testing.T) {
	t.Parallel()
	r, _, _ := newAuthRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `name="username"`)
	handlertest.AssertContains(t, rec, `name="password"`)
	handlertest.AssertContains(t, rec, `name="_csrf"`)
	handlertest.AssertContains(t, rec, middleware.DummyCSRFToken)
}

func TestLogin_Get_NextWithQuery_EncodedInFormAction(t *testing.T) {
	t.Parallel()
	r, _, _ := newAuthRouter(t)

	// next にクエリ文字列を含めて GET。フォームの action がクエリパラメータを
	// 取り違えないよう URL エンコードされていることを確認する。
	rawNext := "/products?tab=all&sort=asc"
	req := httptest.NewRequest(http.MethodGet, "/login?next="+url.QueryEscape(rawNext), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	// form の action 属性に encoded 値が現れること。素のままだと "&sort=asc" が
	// /login のクエリと解釈されてしまう。
	wantAction := `action="/login?next=` + url.QueryEscape(rawNext) + `"`
	handlertest.AssertContains(t, rec, wantAction)
}

func TestLogin_Get_AlreadyAuthenticated_RedirectsToRoot(t *testing.T) {
	t.Parallel()
	r, store, db := newAuthRouter(t)
	ctx := context.Background()

	adminID := seedLocalAdmin(t, db, "admin", "passw0rd")

	// 認証済 session を直接 INSERT
	id, _ := session.NewID()
	tok, _ := session.NewCSRFToken()
	now := time.Now()
	if err := store.Create(ctx, session.Session{
		ID:         id,
		AppUserID:  &adminID,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/login?next=/products", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: id})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/products")
}

func postLogin(t *testing.T, target string, values url.Values) *http.Request {
	t.Helper()
	if values.Get("_csrf") == "" {
		values.Set("_csrf", middleware.DummyCSRFToken)
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestLogin_Post_Success_RedirectsAndBindsSession(t *testing.T) {
	t.Parallel()
	r, store, db := newAuthRouter(t)
	ctx := context.Background()

	adminID := seedLocalAdmin(t, db, "admin", "passw0rd")

	req := postLogin(t, "/login", url.Values{
		"username": {"admin"},
		"password": {"passw0rd"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/")

	// Set-Cookie で新しい session ID が返っている
	var newCookieID string
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			newCookieID = c.Value
		}
	}
	if newCookieID == "" {
		t.Fatal("expected new session Cookie in response")
	}

	got, err := store.GetByID(ctx, newCookieID)
	if err != nil {
		t.Fatalf("GetByID new session: %v", err)
	}
	if got.AppUserID == nil || *got.AppUserID != adminID {
		t.Fatalf("AppUserID = %v, want %d", got.AppUserID, adminID)
	}

	// last_login_at が更新されている
	var lastLogin sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT last_login_at FROM app_users WHERE id = ?`, adminID).Scan(&lastLogin); err != nil {
		t.Fatalf("query last_login_at: %v", err)
	}
	if !lastLogin.Valid {
		t.Fatal("last_login_at should be set after successful login")
	}
}

func TestLogin_Post_Success_RotatesSessionID(t *testing.T) {
	t.Parallel()
	r, store, db := newAuthRouter(t)
	ctx := context.Background()

	seedLocalAdmin(t, db, "admin", "passw0rd")

	// 事前に匿名 session を発行
	preID, _ := session.NewID()
	tok, _ := session.NewCSRFToken()
	now := time.Now()
	if err := store.Create(ctx, session.Session{
		ID:         preID,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed pre-session: %v", err)
	}

	req := postLogin(t, "/login", url.Values{
		"username": {"admin"},
		"password": {"passw0rd"},
	})
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: preID})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusSeeOther)

	// 古い session ID は DB から消えている (Rotate されたので)
	if _, err := store.GetByID(ctx, preID); err == nil {
		t.Fatal("old session ID should be gone after rotation")
	}
}

func TestLogin_Post_WrongPassword_ReturnsError(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)
	seedLocalAdmin(t, db, "admin", "correct-password")

	req := postLogin(t, "/login", url.Values{
		"username": {"admin"},
		"password": {"wrong-password"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ユーザ名またはパスワード")
	// username は復元されているがパスワードは復元されない
	handlertest.AssertContains(t, rec, `value="admin"`)
}

func TestLogin_Post_UnknownUser_ReturnsSameErrorAsWrongPassword(t *testing.T) {
	t.Parallel()
	r, _, _ := newAuthRouter(t)

	req := postLogin(t, "/login", url.Values{
		"username": {"nobody"},
		"password": {"anything"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	// 列挙攻撃対策: 未存在の username と誤パスワードを同じ文言にする
	handlertest.AssertContains(t, rec, "ユーザ名またはパスワード")
}

func TestLogin_Post_DisabledUser_ShowsDisabledMessage(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)

	hash, _ := auth.Hash("passw0rd")
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type, disabled_at) VALUES (?, ?, 'local', ?)`,
		"ex-admin", hash, time.Now())
	if err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	req := postLogin(t, "/login", url.Values{
		"username": {"ex-admin"},
		"password": {"passw0rd"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "無効化")
}

func TestLogin_Post_ADUser_ShowsAuthTypeMessage(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, NULL, 'ad')`,
		"employee01")
	if err != nil {
		t.Fatalf("seed AD: %v", err)
	}

	req := postLogin(t, "/login", url.Values{
		"username": {"employee01"},
		"password": {"anything"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "ローカル認証")
}

func TestLogin_Post_NoCSRF_Returns403(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)
	seedLocalAdmin(t, db, "admin", "passw0rd")

	values := url.Values{"username": {"admin"}, "password": {"passw0rd"}}
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

func TestLogin_Post_NextSameOrigin_RedirectsToPath(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)
	seedLocalAdmin(t, db, "admin", "passw0rd")

	req := postLogin(t, "/login?next=/products", url.Values{
		"username": {"admin"},
		"password": {"passw0rd"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/products")
}

func TestLogin_Post_NextProtocolRelative_Rejected(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)
	seedLocalAdmin(t, db, "admin", "passw0rd")

	req := postLogin(t, "/login?next=//evil.com/path", url.Values{
		"username": {"admin"},
		"password": {"passw0rd"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/")
}

func TestLogin_Post_NextExternalURL_Rejected(t *testing.T) {
	t.Parallel()
	r, _, db := newAuthRouter(t)
	seedLocalAdmin(t, db, "admin", "passw0rd")

	req := postLogin(t, "/login?next=https://evil.com/path", url.Values{
		"username": {"admin"},
		"password": {"passw0rd"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/")
}

func TestLogout_Post_DeletesSessionAndClearsCookie(t *testing.T) {
	t.Parallel()
	r, store, db := newAuthRouter(t)
	ctx := context.Background()

	adminID := seedLocalAdmin(t, db, "admin", "passw0rd")

	id, _ := session.NewID()
	tok, _ := session.NewCSRFToken()
	now := time.Now()
	if err := store.Create(ctx, session.Session{
		ID:         id,
		AppUserID:  &adminID,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	values := url.Values{}
	values.Set("_csrf", middleware.DummyCSRFToken)
	req := httptest.NewRequest(http.MethodPost, "/logout",
		strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: id})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertRedirect(t, rec, "/login")

	if _, err := store.GetByID(ctx, id); err == nil {
		t.Fatal("session should be deleted after logout")
	}

	// Cookie 削除指示 (MaxAge<0)
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected Set-Cookie with MaxAge<0 to clear session cookie")
	}
}
