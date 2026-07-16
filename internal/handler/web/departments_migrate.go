package web

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	deptmigrateview "github.com/tagawa0525/app_man/internal/view/deptmigrate"
)

// departments_migrate.go は部署改廃画面 (仕様 §5.15 / Plan
// department-migrate.md) の web 層:
//
//   - GET  /admin/departments/migrate  廃止部署の選択 → 移管対象プレビュー
//   - POST /admin/departments/migrate  後継部署への一括移管
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。POST は 1 tx で
// licenses の所管一括 UPDATE → アクティブ承認のコピー → audit_logs
// (department.migrate) を行い、記録なしの移管を作らない (承認系と同方針)。
//
// UNIQUE(product_id, owning_department_id, license_slug) に衝突する
// ライセンスと、後継に既存アクティブ承認がある product はスキップして
// 件数報告する (自動リネーム・自動マージは事故源のため、運用者が手動対応
// して再実行するのが正規フロー。再実行は移管済み分が対象 0 件になるだけで
// 安全)。元の承認行は取り消さず廃止部署の履歴として残す (Plan の決定)。
type deptMigrateHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// departmentMigrateDiff は department.migrate の diff_json。entity_id が
// 移管元 (廃止部署) を持つが、from も冗長に含めて 1 行で読めるようにする。
type departmentMigrateDiff struct {
	From             int64 `json:"from"`
	To               int64 `json:"to"`
	LicensesMoved    int64 `json:"licenses_moved"`
	LicensesSkipped  int64 `json:"licenses_skipped"`
	ApprovalsCopied  int64 `json:"approvals_copied"`
	ApprovalsSkipped int64 `json:"approvals_skipped"`
}

// renderForm は選択・プレビュー画面を描画する。fromID / toID が非 0 の
// ときは選択済みとしてプレビュー件数を DB から取り直す (POST 完了後の
// 再描画でも移管後の最新件数を出す)。
func (h *deptMigrateHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, fromID, toID int64, flash string) {
	q := repository.New(h.db)
	retired, err := q.ListRetiredDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list retired departments for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	active, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	props := deptmigrateview.FormProps{
		Retired: retired,
		Active:  active,
		FromID:  fromID,
		ToID:    toID,
		Flash:   flash,
	}
	if fromID != 0 {
		props.LicenseCount, err = q.CountLicensesByDepartment(r.Context(), fromID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "count licenses for migrate preview", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		props.ApprovalCount, err = q.CountActiveApprovalsByDepartment(r.Context(), fromID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "count approvals for migrate preview", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if fromID != 0 && toID != 0 {
		props.ConflictCount, err = q.CountConflictingLicenses(r.Context(), repository.CountConflictingLicensesParams{
			FromDepartmentID: fromID,
			ToDepartmentID:   toID,
		})
		if err != nil {
			h.logger.ErrorContext(r.Context(), "count conflicting licenses for migrate preview", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		props.ShowConflict = true
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := deptmigrateview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render department migrate form", "err", err)
	}
}

// findDeptID は raw (10 進 id) が depts に含まれるならその id を返す。
// 空文字は「未選択」で 0。含まれない・非数値は ok=false。
func findDeptID(raw string, depts []repository.Department) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	for i := range depts {
		if depts[i].ID == id {
			return id, true
		}
	}
	return 0, false
}

// form は GET /admin/departments/migrate。?from= は廃止部署のみ、?to= は
// 現役部署のみ選択可 (直接 URL でも範囲外は 404 — approvals の一覧と同方針)。
func (h *deptMigrateHandlers) form(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	retired, err := q.ListRetiredDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list retired departments for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	active, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	fromID, ok := findDeptID(r.URL.Query().Get("from"), retired)
	if !ok {
		http.NotFound(w, r)
		return
	}
	toID, ok := findDeptID(r.URL.Query().Get("to"), active)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.renderForm(w, r, http.StatusOK, fromID, toID, "")
}

// loadMigrateDept は POST の from / to を検証して部署を返す。誤操作防止の
// 検証 (Plan): from は廃止部署のみ、to は現役部署のみ、from != to。
// 検証エラーは 400 + flash で選択画面を再描画して ok=false。
func (h *deptMigrateHandlers) loadMigrateDept(w http.ResponseWriter, r *http.Request, raw string, wantRetired bool) (repository.Department, bool) {
	var dept repository.Department
	label, requirement := "移管元", "廃止部署"
	if !wantRetired {
		label, requirement = "移管先", "現役部署"
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		h.renderForm(w, r, http.StatusBadRequest, 0, 0, label+"の指定が不正です。")
		return dept, false
	}
	dept, err = repository.New(h.db).GetDepartment(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderForm(w, r, http.StatusBadRequest, 0, 0, label+"の部署が存在しません。")
			return dept, false
		}
		h.logger.ErrorContext(r.Context(), "get department for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return dept, false
	}
	if retired := dept.ValidTo != nil; retired != wantRetired {
		h.renderForm(w, r, http.StatusBadRequest, 0, 0,
			label+"に指定できるのは"+requirement+"のみです。")
		return dept, false
	}
	return dept, true
}

// migrate は POST /admin/departments/migrate。1 tx で licenses の所管
// 一括 UPDATE → アクティブ承認のコピー → audit を行い、結果件数を flash で
// 報告する。
func (h *deptMigrateHandlers) migrate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	from, ok := h.loadMigrateDept(w, r, r.PostFormValue("from"), true)
	if !ok {
		return
	}
	to, ok := h.loadMigrateDept(w, r, r.PostFormValue("to"), false)
	if !ok {
		return
	}
	// from は廃止・to は現役の時点で同一にはなり得ないが、検証の意図を
	// 明示する第 2 層として残す (誤操作防止、Plan)。
	if from.ID == to.ID {
		h.renderForm(w, r, http.StatusBadRequest, 0, 0, "移管元と移管先に同じ部署は指定できません。")
		return
	}

	var approvedBy *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		approvedBy = sess.AppUserID
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for department migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Commit 成功後の Rollback は no-op (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	// slug 衝突行はクエリ側の NOT EXISTS でスキップされる。スキップ数は
	// UPDATE の前後で不変 (移管された行は衝突していない) のため、同一 tx
	// 内で数えて報告に使う。
	moved, err := qtx.MigrateLicensesToDepartment(r.Context(), repository.MigrateLicensesToDepartmentParams{
		ToDepartmentID:   to.ID,
		FromDepartmentID: from.ID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "migrate licenses to department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	licSkipped, err := qtx.CountConflictingLicenses(r.Context(), repository.CountConflictingLicensesParams{
		FromDepartmentID: from.ID,
		ToDepartmentID:   to.ID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count conflicting licenses after migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 承認は後継への新規コピー (元の行は廃止部署の履歴として残す)。後継に
	// 同 product のアクティブ承認が既にあればスキップ (仕様 §5.15
	// 「重複時は手動マージ」)。approval_source は CreateApproval の既定
	// 'direct'、approved_by は実行者、note に移管元を記録する。
	approvals, err := qtx.ListActiveApprovalsForMigration(r.Context(), from.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active approvals for migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	note := "部署改廃により " + from.Name + " から移管"
	var copied, apprSkipped int64
	for _, a := range approvals {
		_, err := qtx.GetActiveApproval(r.Context(), repository.GetActiveApprovalParams{
			DepartmentID: to.ID,
			ProductID:    a.ProductID,
		})
		switch {
		case err == nil:
			apprSkipped++
			continue
		case errors.Is(err, sql.ErrNoRows):
			// コピー対象。
		default:
			h.logger.ErrorContext(r.Context(), "get active approval for migrate", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if _, err := qtx.CreateApproval(r.Context(), repository.CreateApprovalParams{
			DepartmentID:        to.ID,
			ProductID:           a.ProductID,
			Status:              a.Status,
			ScopeType:           a.ScopeType,
			Conditions:          a.Conditions,
			ApprovedByAppUserID: approvedBy,
			ExpiresAt:           a.ExpiresAt,
			Note:                &note,
		}); err != nil {
			h.logger.ErrorContext(r.Context(), "copy approval for migrate", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		copied++
	}

	// 記録なしの移管を作らない: audit INSERT 失敗時は移管ごとロールバック。
	if err := recordAuditDiff(r.Context(), qtx, r, "department.migrate", "department", from.ID,
		departmentMigrateDiff{
			From:             from.ID,
			To:               to.ID,
			LicensesMoved:    moved,
			LicensesSkipped:  licSkipped,
			ApprovalsCopied:  copied,
			ApprovalsSkipped: apprSkipped,
		}); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for department migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit department migrate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	flash := fmt.Sprintf("移管を実行しました。ライセンス移管 %d 件 (スキップ %d 件)、承認コピー %d 件 (スキップ %d 件)。",
		moved, licSkipped, copied, apprSkipped)
	if licSkipped > 0 || apprSkipped > 0 {
		flash += " スキップ分は移管されず残っています。ライセンスは license_slug の変更、承認は手動マージで解消してから再実行してください。"
	}
	h.renderForm(w, r, http.StatusOK, from.ID, to.ID, flash)
}
