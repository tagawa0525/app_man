package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	appuserview "github.com/tagawa0525/app_man/internal/view/appusers"
)

// app_users.go はアプリユーザ管理画面 (仕様 §6.1 / Plan
// admin-app-users-roles.md) の web 層:
//
//   - GET  /admin/app-users                     一覧
//   - POST /admin/app-users/{id}/disable        無効化
//   - POST /admin/app-users/{id}/enable         再有効化
//   - POST /admin/app-users/{id}/notify-email   通知先メール変更
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。作成・パスワード
// リセット UI は持たない (CLI appmgr-create-app-user の責務、Plan)。
//
// 無効化と既存セッション: AuthMiddleware は毎リクエスト
// user_department_roles しか引かず app_users.disabled_at を見ない
// (disabled はログイン時に Authenticator が弾くだけ)。そのため無効化と
// 同一トランザクションで対象ユーザのセッションを DB から削除し、既存
// Cookie での操作継続を防ぐ (Plan 想定リスク欄の調査結果)。
//
// 自分自身の無効化は 400: 唯一の system_admin が自分を無効化して全員
// ロックアウトする最短経路を塞ぐ。無効化してもロール行は触らない
// (再有効化で元の権限に戻る方が運用事故が少ない、Plan)。
type appUserHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// appUserNotifyEmailDiff は app_user.notify_email_change の diff_json。
// 未設定 (NULL) は old / new とも omitempty で省略する。
type appUserNotifyEmailDiff struct {
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// renderList は一覧を描画する。400 / 409 の失敗もフラッシュ付きで一覧を
// 再表示する (settings と同方針)。
func (h *appUserHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, flash string) {
	q := repository.New(h.db)
	rows, err := q.ListAppUsersWithRoleCount(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list app users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	props := appuserview.ListProps{Flash: flash}
	for _, row := range rows {
		v := appuserview.Row{
			ID:              row.ID,
			Username:        row.Username,
			AuthType:        row.AuthType,
			LinkedUserName:  "-",
			Disabled:        row.DisabledAt != nil,
			ActiveRoleCount: row.ActiveRoleCount,
		}
		if row.LinkedUserName != nil {
			v.LinkedUserName = *row.LinkedUserName
		}
		if row.NotifyEmail != nil {
			v.NotifyEmail = *row.NotifyEmail
		}
		props.Rows = append(props.Rows, v)
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := appuserview.List(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render app users list", "err", err)
	}
}

// list は GET /admin/app-users。
func (h *appUserHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, "")
}

// disable は POST /admin/app-users/{id}/disable。disabled_at 設定 +
// セッション削除 + audit (app_user.disable) を 1 tx で行う。
func (h *appUserHandlers) disable(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if sess := middleware.SessionFrom(r.Context()); sess != nil && sess.AppUserID != nil && *sess.AppUserID == id {
		h.renderList(w, r, http.StatusBadRequest, "自分自身は無効化できません。")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for app user disable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Commit 成功後の Rollback は no-op (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	u, err := qtx.GetAppUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user before disable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u.DisabledAt != nil {
		// テンプレ描画中に tx (接続) を保持し続けないよう先に閉じる。
		_ = tx.Rollback()
		h.renderList(w, r, http.StatusConflict, "このユーザは既に無効化されています。")
		return
	}
	affected, err := qtx.DisableAppUser(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "disable app user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		// GetAppUser 通過後に並行無効化されたレース。
		_ = tx.Rollback()
		h.renderList(w, r, http.StatusConflict, "このユーザは既に無効化されています。")
		return
	}
	// AuthMiddleware は disabled_at を毎リクエストでは見ないため、既存
	// セッションをここで削除しないと無効化後も操作を継続できてしまう。
	if _, err := qtx.DeleteSessionsForAppUser(r.Context(), &id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete sessions for disabled app user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 記録なしの変更を作らない: audit INSERT 失敗時は無効化ごとロールバック。
	if err := recordAudit(r.Context(), qtx, r, "app_user.disable", "app_user", id); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for app user disable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit app user disable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/app-users", http.StatusSeeOther)
}

// enable は POST /admin/app-users/{id}/enable。disabled_at を NULL に戻し
// audit (app_user.enable) を記録する。ロール行は触らない (無効化時に
// 残したままなので、再有効化で元の権限に戻る)。
func (h *appUserHandlers) enable(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for app user enable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	u, err := qtx.GetAppUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user before enable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u.DisabledAt == nil {
		_ = tx.Rollback()
		h.renderList(w, r, http.StatusConflict, "このユーザは既に有効です。")
		return
	}
	affected, err := qtx.EnableAppUser(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "enable app user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		_ = tx.Rollback()
		h.renderList(w, r, http.StatusConflict, "このユーザは既に有効です。")
		return
	}
	if err := recordAudit(r.Context(), qtx, r, "app_user.enable", "app_user", id); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for app user enable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit app user enable", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/app-users", http.StatusSeeOther)
}

// updateNotifyEmail は POST /admin/app-users/{id}/notify-email。
// TrimSpace 後、空なら NULL、非空なら「@ を含む」軽量検証のみ (本格
// 検証は通知フェーズの責務、Plan)。audit は old / new を記録する。
func (h *appUserHandlers) updateNotifyEmail(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	raw := strings.TrimSpace(r.PostFormValue("notify_email"))
	if raw != "" && !strings.Contains(raw, "@") {
		h.renderList(w, r, http.StatusBadRequest, "通知先メールが不正です。@ を含むアドレスを入力してください。")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for notify email change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	// diff_json の old を取るため、同一 tx 内で変更前の値を読む。
	u, err := qtx.GetAppUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user before notify email change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	diff := appUserNotifyEmailDiff{New: raw}
	if u.NotifyEmail != nil {
		diff.Old = *u.NotifyEmail
	}

	if _, err := qtx.UpdateAppUserNotifyEmail(r.Context(), repository.UpdateAppUserNotifyEmailParams{
		NotifyEmail: nilIfEmpty(raw),
		ID:          id,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "update notify email", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := recordAuditDiff(r.Context(), qtx, r, "app_user.notify_email_change", "app_user", id, diff); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for notify email change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit notify email change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/app-users", http.StatusSeeOther)
}
