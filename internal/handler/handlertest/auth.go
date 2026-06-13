package handlertest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/session"
)

// authUserCounter は AuthenticatedAs が組み立てる test username の連番。
// t.Parallel() で複数テストが同時に同じ DB を触らないが、複数 DB 内で
// 同じ username を使ってもユーザ識別の見通しが落ちるため、グローバルに
// 一意な連番でユニーク性を確保する。
var authUserCounter int64

// AuthenticatedAs は role を持つ app_user と session を 1 つずつ作成し、
// session Cookie を返す。テストは httptest.NewRequest に AddCookie するだけで
// 「SessionMiddleware + AuthMiddleware を通った後にその role が context に
// 入っている」状態を再現できる。
//
// 引数:
//   - db: handlertest.NewTestDB(t) で作った in-memory sqlite
//   - store: session.NewSQLiteStore(db) で作ったストア
//   - role: 付与する単一 role (department NULL)
//
// 戻り値の Cookie は HttpOnly や SameSite を設定しない (テストでは不要、
// 値だけ session.CookieName=<id> として認識されれば十分)。
func AuthenticatedAs(t *testing.T, db *sql.DB, store session.Store, role middleware.Role) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	n := atomic.AddInt64(&authUserCounter, 1)

	username := fmt.Sprintf("test_%s_%d", role, n)
	res, err := db.ExecContext(ctx,
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, '', 'local')`,
		username)
	if err != nil {
		t.Fatalf("AuthenticatedAs: insert app_users: %v", err)
	}
	appUserID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("AuthenticatedAs: LastInsertId: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO user_department_roles (app_user_id, department_id, role) VALUES (?, NULL, ?)`,
		appUserID, string(role)); err != nil {
		t.Fatalf("AuthenticatedAs: insert user_department_roles: %v", err)
	}

	id, err := session.NewID()
	if err != nil {
		t.Fatalf("AuthenticatedAs: NewID: %v", err)
	}
	tok, err := session.NewCSRFToken()
	if err != nil {
		t.Fatalf("AuthenticatedAs: NewCSRFToken: %v", err)
	}
	now := time.Now()
	if err := store.Create(ctx, session.Session{
		ID:         id,
		AppUserID:  &appUserID,
		CSRFToken:  tok,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("AuthenticatedAs: store.Create: %v", err)
	}

	return &http.Cookie{Name: session.CookieName, Value: id}
}

// AuthenticatedRequest は AuthenticatedAs で作った session Cookie を付けた
// *http.Request を返す。role="" の場合は session を作らず未認証リクエストを返す
// (AuthMiddleware が /login にリダイレクトする経路の検証に使える)。
//
// SessionMiddleware + AuthMiddleware + CSRFMiddleware が貼られた chi.Router
// に投げる前提。
func AuthenticatedRequest(t *testing.T, db *sql.DB, store session.Store,
	method, target string, role middleware.Role, body io.Reader,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if role != "" {
		req.AddCookie(AuthenticatedAs(t, db, store, role))
	}
	return req
}

// AuthenticatedPostForm は AuthenticatedAs で作った session Cookie を付け、
// その session の CSRFToken を form の _csrf に埋めた POST リクエストを返す。
// values に _csrf が含まれていればそちらを尊重する (CSRF mismatch の
// 検証テストでカスタム値を渡す経路)。
//
// role="" を渡した場合は session を作らず _csrf も埋めない
// (CSRFMiddleware が 403 を返す経路の検証用)。
func AuthenticatedPostForm(t *testing.T, db *sql.DB, store session.Store,
	target string, role middleware.Role, values url.Values,
) *http.Request {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	req := httptest.NewRequest(http.MethodPost, target, nil)
	if role != "" {
		cookie := AuthenticatedAs(t, db, store, role)
		req.AddCookie(cookie)
		if values.Get("_csrf") == "" {
			sess, err := store.GetByID(req.Context(), cookie.Value)
			if err != nil {
				t.Fatalf("AuthenticatedPostForm: GetByID(%q): %v", cookie.Value, err)
			}
			values.Set("_csrf", sess.CSRFToken)
		}
	}
	req.Body = io.NopCloser(strings.NewReader(values.Encode()))
	req.ContentLength = int64(len(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
