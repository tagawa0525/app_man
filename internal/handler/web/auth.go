package web

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/session"
	authview "github.com/tagawa0525/app_man/internal/view/auth"
)

// authHandlers は /login GET / POST と /logout POST を束ねる。
//
// 依存:
//
//   - authenticator: 認証ロジックの境界。本 PR では LocalAuthenticator が
//     直接渡るが、後続 PR で Composite に切り替えやすいよう interface 受け
//   - sessionStore: ログイン成功時に session ID を Rotate + app_user_id を埋める
//   - db: トランザクションを張って Rotate / BindSessionToAppUser /
//     UpdateAppUserLastLoginAt を 1 まとまりで実行する
//   - cookieSecure: Cookie の Secure 属性
//   - sessionMaxAge: 新規発行 Cookie の MaxAge (rotate 後の Cookie に使う)
type authHandlers struct {
	authenticator auth.Authenticator
	sessionStore  session.Store
	db            *sql.DB
	cookieSecure  bool
	sessionMaxAge time.Duration
	logger        *slog.Logger
}

const (
	msgInvalidCredentials  = "ユーザ名またはパスワードが正しくありません。"
	msgUserDisabled        = "アカウントが無効化されています。管理者にお問い合わせください。"
	msgUnsupportedAuthType = "ローカル認証用のアカウントではありません。"
	msgInternal            = "ログイン処理に失敗しました。時間をおいて再度お試しください。"
)

// loginGet は GET /login。
// 既にログイン済 (session.AppUserID != nil) なら ?next= or "/" にリダイレクト、
// それ以外はログインフォームを 200 で返す。
func (h *authHandlers) loginGet(w http.ResponseWriter, r *http.Request) {
	next := validateNext(r.URL.Query().Get("next"))

	if sess := middleware.SessionFrom(r.Context()); sess != nil && sess.AppUserID != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	h.renderLogin(w, r, authview.LoginProps{
		CSRFToken: middleware.DummyCSRFToken,
		Next:      next,
	}, http.StatusOK)
}

// loginPost は POST /login。CSRFMiddleware は通過済み (上流で検査)。
// 成功時:
//  1. tx 内で session.Rotate (oldID → newID)、BindSessionToAppUser (newID, userID)、
//     UpdateAppUserLastLoginAt (userID, now) を実行
//  2. Cookie を newID で上書き
//  3. ?next= or "/" に 303
//
// 失敗時はエラー種別に応じた文言で login form を 200 で再表示。
func (h *authHandlers) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.WarnContext(r.Context(), "login parse form", "err", err)
		h.renderLogin(w, r, authview.LoginProps{
			CSRFToken:    middleware.DummyCSRFToken,
			ErrorMessage: msgInternal,
		}, http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	next := validateNext(r.URL.Query().Get("next"))

	user, err := h.authenticator.Authenticate(r.Context(), username, password)
	if err != nil {
		h.renderAuthError(w, r, username, next, err)
		return
	}

	sess := middleware.SessionFrom(r.Context())
	if sess == nil || sess.ID == "" {
		// SessionMiddleware が必ず session を発行している前提なので、
		// これに来るのは router の組立ミスか ephemeral session のケース。
		h.logger.ErrorContext(r.Context(), "login: no session in context")
		h.renderLogin(w, r, authview.LoginProps{
			CSRFToken:    middleware.DummyCSRFToken,
			Username:     username,
			Next:         next,
			ErrorMessage: msgInternal,
		}, http.StatusInternalServerError)
		return
	}

	newID, err := h.rotateAndBind(r.Context(), sess.ID, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "login: rotate+bind tx", "err", err, "username", username)
		h.renderLogin(w, r, authview.LoginProps{
			CSRFToken:    middleware.DummyCSRFToken,
			Username:     username,
			Next:         next,
			ErrorMessage: msgInternal,
		}, http.StatusInternalServerError)
		return
	}

	session.SetCookie(w, newID, h.sessionMaxAge, h.cookieSecure)
	h.logger.InfoContext(r.Context(), "login success",
		"username", user.Username, "app_user_id", user.ID)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// logoutPost は POST /logout。session を DB から削除し Cookie を消去、
// /login に 303 で戻す。CSRF は CSRFMiddleware が検証済み。
func (h *authHandlers) logoutPost(w http.ResponseWriter, r *http.Request) {
	if sess := middleware.SessionFrom(r.Context()); sess != nil && sess.ID != "" {
		if err := h.sessionStore.Delete(r.Context(), sess.ID); err != nil {
			h.logger.WarnContext(r.Context(), "logout: store.Delete", "err", err)
		}
		h.logger.InfoContext(r.Context(), "logout", "app_user_id", sess.AppUserID)
	}
	session.ClearCookie(w, h.cookieSecure)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// rotateAndBind は session ID 再発行 + app_user_id 結びつけ + last_login_at 更新を
// 1 トランザクションで行う。途中失敗で session ID だけ変わって app_user_id が
// 埋まらない、を防ぐ。
func (h *authHandlers) rotateAndBind(ctx context.Context, oldSessionID string, appUserID int64) (string, error) {
	newID, err := session.NewID()
	if err != nil {
		return "", err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	q := repository.New(tx)
	if err := q.RotateSessionID(ctx, repository.RotateSessionIDParams{NewID: newID, OldID: oldSessionID}); err != nil {
		return "", err
	}
	if err := q.BindSessionToAppUser(ctx, repository.BindSessionToAppUserParams{AppUserID: &appUserID, ID: newID}); err != nil {
		return "", err
	}
	now := time.Now()
	if err := q.UpdateAppUserLastLoginAt(ctx, repository.UpdateAppUserLastLoginAtParams{LastLoginAt: &now, ID: appUserID}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newID, nil
}

// renderAuthError はエラー種別ごとの文言を選んで login form を再表示する。
func (h *authHandlers) renderAuthError(w http.ResponseWriter, r *http.Request, username, next string, authErr error) {
	var (
		msg    string
		status = http.StatusOK
	)
	switch {
	case errors.Is(authErr, auth.ErrInvalidCredentials):
		msg = msgInvalidCredentials
		h.logger.InfoContext(r.Context(), "login failed",
			"username", username, "reason", "invalid_credentials")
	case errors.Is(authErr, auth.ErrUserDisabled):
		msg = msgUserDisabled
		h.logger.InfoContext(r.Context(), "login failed",
			"username", username, "reason", "user_disabled")
	case errors.Is(authErr, auth.ErrUnsupportedAuthType):
		msg = msgUnsupportedAuthType
		h.logger.InfoContext(r.Context(), "login failed",
			"username", username, "reason", "unsupported_auth_type")
	default:
		h.logger.ErrorContext(r.Context(), "login: authenticator", "err", authErr)
		msg = msgInternal
		status = http.StatusInternalServerError
	}
	h.renderLogin(w, r, authview.LoginProps{
		CSRFToken:    middleware.DummyCSRFToken,
		Username:     username,
		Next:         next,
		ErrorMessage: msg,
	}, status)
}

func (h *authHandlers) renderLogin(w http.ResponseWriter, r *http.Request, p authview.LoginProps, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := authview.LoginPage(p).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render login", "err", err)
	}
}

// validateNext は ?next= で渡される遷移先を同一オリジン (絶対パス、先頭 "/" 必須、
// "//" 排除、外部 URL 排除) のみ通す。空 / 不正なら "/" を返す。
func validateNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return u.RequestURI()
}
