package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	roleview "github.com/tagawa0525/app_man/internal/view/roles"
)

// roles.go はロール管理画面 (仕様 §6.1 / Plan admin-app-users-roles.md)
// の web 層:
//
//   - GET  /admin/roles?app_user_id=                 app_user 選択 + アクティブ
//     ロール一覧 + 付与フォーム
//   - POST /admin/roles/{appUserID}                  付与
//   - POST /admin/roles/{appUserID}/{roleID}/revoke  剥奪
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。付与の検証は
// create-app-user CLI (resolveDepartmentID / IsValidRole) と同じ規則:
// role は AllRoles のみ、system_admin は department NULL 強制、他ロールは
// 現役部署必須 (不存在・廃止は 400)。アクティブ重複は事前チェックで 409
// とし、000006 の部分 UNIQUE 2 本立て (dept NOT NULL / NULL) をレース時の
// 最終防衛にする。
//
// 剥奪のロックアウト防止は 2 層 (Plan):
//
//  1. 自分の system_admin ロールは剥奪不可 (400)。他に有効な admin が
//     いても誤操作での自己降格を塞ぐ
//  2. 「アクティブ system_admin ロールを持つ有効 (disabled_at IS NULL)
//     ユーザ」が 1 人以下なら system_admin ロールは剥奪不可 (400)。
//     無効化ユーザは勘定から除外する (無効化はロール行を残すため)
type roleHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// roleChangeDiff は role.grant / role.revoke の diff_json。全社スコープは
// department_id: null で記録する (省略すると「部署不明」と区別できない)。
type roleChangeDiff struct {
	Role         string `json:"role"`
	DepartmentID *int64 `json:"department_id"`
}

// renderRoles は画面を描画する。selected が nil なら app_user 未選択。
func (h *roleHandlers) renderRoles(w http.ResponseWriter, r *http.Request, status int, selected *repository.AppUser, flash string) {
	q := repository.New(h.db)
	users, err := q.ListAppUsersWithRoleCount(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list app users for roles", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	props := roleview.ListProps{Flash: flash}
	for _, u := range users {
		props.Users = append(props.Users, roleview.UserOption{
			ID:       u.ID,
			Username: u.Username,
			Disabled: u.DisabledAt != nil,
		})
	}

	if selected != nil {
		props.SelectedID = selected.ID
		props.SelectedUsername = selected.Username
		rows, err := q.ListActiveRolesWithDepartmentForAppUser(r.Context(), selected.ID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list active roles for app user", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			v := roleview.RoleRow{
				ID:             row.ID,
				Role:           row.Role,
				DepartmentName: "全社",
				GrantedAt:      row.GrantedAt.In(auditLogsJST).Format("2006-01-02 15:04:05"),
			}
			if row.DepartmentName != nil {
				v.DepartmentName = *row.DepartmentName
			}
			props.Roles = append(props.Roles, v)
		}
		props.Departments, err = q.ListActiveDepartments(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list active departments for roles", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := roleview.List(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render roles list", "err", err)
	}
}

// list は GET /admin/roles。?app_user_id= が不正・不存在なら 404
// (approvals の部署選択と同方針)。
func (h *roleHandlers) list(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("app_user_id"))
	if raw == "" {
		h.renderRoles(w, r, http.StatusOK, nil, "")
		return
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	u, err := repository.New(h.db).GetAppUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user for roles", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderRoles(w, r, http.StatusOK, &u, "")
}

// grant は POST /admin/roles/{appUserID}。
func (h *roleHandlers) grant(w http.ResponseWriter, r *http.Request) {
	appUserID, ok := parseInt64Param(r, "appUserID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	u, err := q.GetAppUser(r.Context(), appUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user for role grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = r.ParseForm()
	roleStr := strings.TrimSpace(r.PostFormValue("role"))
	if !middleware.IsValidRole(middleware.Role(roleStr)) {
		h.renderRoles(w, r, http.StatusBadRequest, &u, "ロールの指定が不正です。")
		return
	}
	deptRaw := strings.TrimSpace(r.PostFormValue("department_id"))
	var departmentID *int64
	if roleStr == string(middleware.RoleSystemAdmin) {
		// system_admin は全社スコープ固定 (create-app-user CLI と同じ)。
		if deptRaw != "" {
			h.renderRoles(w, r, http.StatusBadRequest, &u,
				"system_admin は全社スコープ固定のため部署を指定できません。")
			return
		}
	} else {
		if deptRaw == "" {
			h.renderRoles(w, r, http.StatusBadRequest, &u, "このロールには部署の指定が必須です。")
			return
		}
		deptID, perr := strconv.ParseInt(deptRaw, 10, 64)
		if perr != nil || deptID <= 0 {
			h.renderRoles(w, r, http.StatusBadRequest, &u, "部署の指定が不正です。")
			return
		}
		dept, derr := q.GetDepartment(r.Context(), deptID)
		if derr != nil {
			if errors.Is(derr, sql.ErrNoRows) {
				h.renderRoles(w, r, http.StatusBadRequest, &u, "指定された部署が存在しません。")
				return
			}
			h.logger.ErrorContext(r.Context(), "get department for role grant", "err", derr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// 廃止部署へのロール付与は事故の可能性が高い (CLI と同方針)。
		if dept.ValidTo != nil {
			h.renderRoles(w, r, http.StatusBadRequest, &u, "廃止済みの部署にはロールを付与できません。")
			return
		}
		departmentID = &dept.ID
	}

	// アクティブ重複は事前チェックで 409。並行付与のレースは部分 UNIQUE
	// (uniq_user_dept_roles_active_dept / _global) が最終防衛する。
	dup, err := q.CountActiveUserDepartmentRoles(r.Context(), repository.CountActiveUserDepartmentRolesParams{
		AppUserID:    appUserID,
		Role:         roleStr,
		DepartmentID: departmentID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count active roles before grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if dup > 0 {
		h.renderRoles(w, r, http.StatusConflict, &u, "同じスコープのアクティブなロールが既にあります。")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for role grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Commit 成功後の Rollback は no-op (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()
	qtx := q.WithTx(tx)

	created, err := qtx.CreateUserDepartmentRole(r.Context(), repository.CreateUserDepartmentRoleParams{
		AppUserID:    appUserID,
		DepartmentID: departmentID,
		Role:         roleStr,
	})
	if err != nil {
		// テンプレ描画中に tx (接続) を保持し続けないよう先に閉じる。
		_ = tx.Rollback()
		if isUniqueConstraintErr(err) {
			// 事前チェック通過後に並行付与されたレース。
			h.renderRoles(w, r, http.StatusConflict, &u, "同じスコープのアクティブなロールが既にあります。")
			return
		}
		h.logger.ErrorContext(r.Context(), "create user department role", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 記録なしの権限変更を作らない: audit INSERT 失敗時は付与ごとロールバック。
	if err := recordAuditDiff(r.Context(), qtx, r, "role.grant", "user_department_role", created.ID,
		roleChangeDiff{Role: created.Role, DepartmentID: created.DepartmentID}); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for role grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit role grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, rolesPagePath(appUserID), http.StatusSeeOther)
}

// revoke は POST /admin/roles/{appUserID}/{roleID}/revoke。
func (h *roleHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	appUserID, ok := parseInt64Param(r, "appUserID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	roleID, ok := parseInt64Param(r, "roleID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	u, err := q.GetAppUser(r.Context(), appUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get app user for role revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	row, err := q.GetUserDepartmentRole(r.Context(), roleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get user department role", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// URL の appUserID と行の持ち主が食い違う・剥奪済みは 404 (存在しない
	// アクティブロールと同じ扱い)。
	if row.AppUserID != appUserID || row.RevokedAt != nil {
		http.NotFound(w, r)
		return
	}

	// ロックアウト防止 1 層目: 自分の system_admin ロールは剥奪不可。
	// レースと無関係の方針判定なので事前チェックのまま置く。2 層目
	// (最後の有効 system_admin) は RevokeUserDepartmentRole の WHERE に
	// 埋め込んだガードが UPDATE 実行時 (書込みロック下) に原子的に判定
	// する — handler の COUNT → UPDATE 2 手だと WAL の並行 write tx が
	// 双方 COUNT=2 を観測して有効 admin 0 人になり得るため使わない。
	if row.Role == string(middleware.RoleSystemAdmin) {
		if sess := middleware.SessionFrom(r.Context()); sess != nil && sess.AppUserID != nil && *sess.AppUserID == appUserID {
			h.renderRoles(w, r, http.StatusBadRequest, &u, "自分自身の system_admin ロールは剥奪できません。")
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for role revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := q.WithTx(tx)

	affected, err := qtx.RevokeUserDepartmentRole(r.Context(), repository.RevokeUserDepartmentRoleParams{
		ID:        roleID,
		AppUserID: appUserID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke user department role", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		// ガード付き UPDATE の 0 行は 2 系統: (a) 並行剥奪で行が既に
		// inactive、(b) last-admin ガードで弾かれた。行の存在・active・
		// system_admin か・有効 admin 数を読み直して 404 / 400 を出し
		// 分ける。
		_ = tx.Rollback()
		latest, lerr := q.GetUserDepartmentRole(r.Context(), roleID)
		if lerr != nil {
			if errors.Is(lerr, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			h.logger.ErrorContext(r.Context(), "re-read role after guarded revoke", "err", lerr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if latest.AppUserID == appUserID && latest.RevokedAt == nil {
			// 行がまだアクティブなのに 0 行 = ガード付き UPDATE の
			// last-admin 条件に弾かれたケースしかない (UPDATE 時点の
			// 判定が正)。事後に admin 数を読み直しても UPDATE 時点と
			// ズレうるので再カウントせず 400 を返す。
			h.renderRoles(w, r, http.StatusBadRequest, &u,
				"有効な system_admin が 1 人しかいないため剥奪できません。先に別の system_admin を用意してください。")
			return
		}
		// 行が消えている / 既に剥奪済み (並行操作) は 404。
		http.NotFound(w, r)
		return
	}
	if err := recordAuditDiff(r.Context(), qtx, r, "role.revoke", "user_department_role", roleID,
		roleChangeDiff{Role: row.Role, DepartmentID: row.DepartmentID}); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for role revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit role revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, rolesPagePath(appUserID), http.StatusSeeOther)
}

// rolesPagePath は app_user 選択済みのロール管理画面 URL。
func rolesPagePath(appUserID int64) string {
	return "/admin/roles?app_user_id=" + strconv.FormatInt(appUserID, 10)
}
