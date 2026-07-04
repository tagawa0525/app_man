package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/approval"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	approvalview "github.com/tagawa0525/app_man/internal/view/approvals"
)

// approvals.go は承認管理 3 画面 (仕様 §6.1 / Plan approvals.md) の web 層:
//
//   - GET  /approvals                              部署選択 + 製品 × 承認状態一覧
//   - GET  /approvals/{deptID}/{productID}         登録・編集画面 (履歴 + フォーム)
//   - POST /approvals/{deptID}/{productID}         登録 (既存アクティブは 409)
//   - POST /approvals/{deptID}/{productID}/revoke  取消 (revoke_reason 必須)
//   - GET/POST /admin/global-approvals             全社既定の変更 (system_admin)
//
// 変更 = 取消 + 新規 (uniq_dept_product_approvals_active のため)。
// 登録・取消・全社変更は audit_logs への記録と同一トランザクションで行い、
// 記録なしの操作を作らない (キー閲覧と同方針、bootstrap の原子化と同方針)。
type approvalHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// list は GET /approvals。?department_id= で現役部署を選び、全製品について
// approval.Evaluate の結果を表示する。specific_* スコープは部署単位表示の
// ため InScope を評価しない (rec の status/scope をそのまま渡し InScope=false
// → 表示上は未承認)。代わりに行へ scope_type を注記する。
func (h *approvalHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for approvals", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var selected *repository.Department
	if raw := strings.TrimSpace(r.URL.Query().Get("department_id")); raw != "" {
		id, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		for i := range depts {
			if depts[i].ID == id {
				selected = &depts[i]
				break
			}
		}
		if selected == nil {
			// 廃止済み or 存在しない部署は選択不可 (現役のみ)。
			http.NotFound(w, r)
			return
		}
	}

	props := approvalview.ListProps{Departments: depts}
	if selected != nil {
		props.SelectedID = selected.ID
		products, err := q.ListProducts(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list products for approvals", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rows, err := q.ListApprovalsForDepartment(r.Context(), selected.ID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list approvals for department", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		recByProduct := make(map[int64]repository.ListApprovalsForDepartmentRow, len(rows))
		for _, row := range rows {
			recByProduct[row.ProductID] = row
		}
		now := time.Now()
		for _, p := range products {
			var rec *approval.Record
			scopeNote := ""
			if row, ok := recByProduct[p.ID]; ok {
				rec = &approval.Record{
					Status:    row.Status,
					ScopeType: row.ScopeType,
					ExpiresAt: row.ExpiresAt,
				}
				if row.ScopeType != approval.ScopeDepartmentWide {
					scopeNote = row.ScopeType
				}
			}
			props.Items = append(props.Items, approvalview.ListItem{
				Product:   p,
				Verdict:   approval.Evaluate(p.DefaultApprovalStatus, rec, now),
				ScopeNote: scopeNote,
			})
		}
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := approvalview.List(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render approvals list", "err", err)
	}
}

// loadDeptProduct は {deptID}/{productID} を解決する。どちらかが不正・
// 不存在なら 404 を書き込んで ok=false を返す。
func (h *approvalHandlers) loadDeptProduct(w http.ResponseWriter, r *http.Request) (repository.Department, repository.GetProductRow, bool) {
	var (
		dept    repository.Department
		product repository.GetProductRow
	)
	deptID, ok := parseInt64Param(r, "deptID")
	if !ok {
		http.NotFound(w, r)
		return dept, product, false
	}
	productID, ok := parseInt64Param(r, "productID")
	if !ok {
		http.NotFound(w, r)
		return dept, product, false
	}
	q := repository.New(h.db)
	dept, err := q.GetDepartment(r.Context(), deptID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return dept, product, false
		}
		h.logger.ErrorContext(r.Context(), "get department for approval", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return dept, product, false
	}
	product, err = q.GetProduct(r.Context(), productID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return dept, product, false
		}
		h.logger.ErrorContext(r.Context(), "get product for approval", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return dept, product, false
	}
	return dept, product, true
}

// renderDetail は登録・編集画面を描画する。アクティブ承認と履歴は毎回
// DB から取り直す (取消・登録の失敗時再表示でも最新の状態を出す)。
func (h *approvalHandlers) renderDetail(w http.ResponseWriter, r *http.Request, status int,
	dept repository.Department, product repository.GetProductRow,
	input approvalview.FormInput, errs map[string]string, flash string,
) {
	q := repository.New(h.db)
	var active *repository.DepartmentProductApproval
	a, err := q.GetActiveApproval(r.Context(), repository.GetActiveApprovalParams{
		DepartmentID: dept.ID,
		ProductID:    product.ID,
	})
	switch {
	case err == nil:
		active = &a
	case errors.Is(err, sql.ErrNoRows):
		// アクティブ承認なし → 登録フォームを出す。
	default:
		h.logger.ErrorContext(r.Context(), "get active approval", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	history, err := q.ListApprovalHistoryForDeptProduct(r.Context(), repository.ListApprovalHistoryForDeptProductParams{
		DepartmentID: dept.ID,
		ProductID:    product.ID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list approval history", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := approvalview.Detail(role, approvalview.DetailProps{
		Department: dept,
		Product:    product,
		Active:     active,
		History:    history,
		Input:      input,
		Errors:     errs,
		Flash:      flash,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render approval detail", "err", err)
	}
}

// show は GET /approvals/{deptID}/{productID}。
func (h *approvalHandlers) show(w http.ResponseWriter, r *http.Request) {
	dept, product, ok := h.loadDeptProduct(w, r)
	if !ok {
		return
	}
	h.renderDetail(w, r, http.StatusOK, dept, product, approvalview.FormInput{}, nil, "")
}

// decodeApprovalForm は登録フォームを検証する。expires_at は任意の
// YYYY-MM-DD (パース結果を第 2 戻り値で返す)。
func decodeApprovalForm(r *http.Request) (approvalview.FormInput, *time.Time, map[string]string) {
	_ = r.ParseForm()
	in := approvalview.FormInput{
		Status:     strings.TrimSpace(r.PostFormValue("status")),
		Conditions: strings.TrimSpace(r.PostFormValue("conditions")),
		ExpiresAt:  strings.TrimSpace(r.PostFormValue("expires_at")),
		Note:       r.PostFormValue("note"),
	}
	errs := map[string]string{}
	switch in.Status {
	case approval.StatusApproved, approval.StatusConditional, approval.StatusProhibited:
	default:
		errs["status"] = "承認状態の指定が不正です"
	}
	if in.Status == approval.StatusConditional && in.Conditions == "" {
		errs["conditions"] = "条件付き承認では条件の入力が必須です"
	}
	var expires *time.Time
	if in.ExpiresAt != "" {
		t, err := time.Parse("2006-01-02", in.ExpiresAt)
		if err != nil {
			errs["expires_at"] = "有効期限は YYYY-MM-DD 形式で入力してください"
		} else {
			expires = &t
		}
	}
	return in, expires, errs
}

// create は POST /approvals/{deptID}/{productID}。scope_type は MVP では
// department_wide 固定 (specific_* の設定 UI は範囲外)。既存アクティブが
// ある場合は 409 で「先に取消」を促す。INSERT と audit_logs は 1 tx。
func (h *approvalHandlers) create(w http.ResponseWriter, r *http.Request) {
	dept, product, ok := h.loadDeptProduct(w, r)
	if !ok {
		return
	}
	input, expiresAt, errs := decodeApprovalForm(r)
	if len(errs) > 0 {
		h.renderDetail(w, r, http.StatusBadRequest, dept, product, input, errs, "")
		return
	}

	q := repository.New(h.db)
	if _, err := q.GetActiveApproval(r.Context(), repository.GetActiveApprovalParams{
		DepartmentID: dept.ID,
		ProductID:    product.ID,
	}); err == nil {
		h.renderDetail(w, r, http.StatusConflict, dept, product, input, nil,
			"既にアクティブな承認があります。変更するには先に取消してから再登録してください。")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.logger.ErrorContext(r.Context(), "get active approval before create", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var approvedBy *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		approvedBy = sess.AppUserID
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for approval grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Commit 成功後の Rollback は no-op (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()
	qtx := q.WithTx(tx)

	created, err := qtx.CreateApproval(r.Context(), repository.CreateApprovalParams{
		DepartmentID:        dept.ID,
		ProductID:           product.ID,
		Status:              input.Status,
		ScopeType:           approval.ScopeDepartmentWide,
		Conditions:          nilIfEmpty(input.Conditions),
		ApprovedByAppUserID: approvedBy,
		ExpiresAt:           expiresAt,
		Note:                nilIfEmpty(strings.TrimSpace(input.Note)),
	})
	if err != nil {
		// テンプレ描画中に tx (接続) を保持し続けないよう先に閉じる。
		_ = tx.Rollback()
		if isUniqueConstraintErr(err) {
			// GetActiveApproval 通過後に並行登録されたレース。
			h.renderDetail(w, r, http.StatusConflict, dept, product, input, nil,
				"既にアクティブな承認があります。変更するには先に取消してから再登録してください。")
			return
		}
		h.logger.ErrorContext(r.Context(), "create approval", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 記録なしの承認を作らない: audit INSERT 失敗時は登録ごとロールバック。
	if err := recordAudit(r.Context(), qtx, r, "approval.grant", "department_product_approval", created.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for approval grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit approval grant", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, approvalPath(dept.ID, product.ID), http.StatusSeeOther)
}

// revoke は POST /approvals/{deptID}/{productID}/revoke。revoke_reason は
// 内部統制の理由記録として必須 (空白のみは 400)。UPDATE と audit_logs は
// 1 tx。0 行更新 (アクティブなし) は 404。
func (h *approvalHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	dept, product, ok := h.loadDeptProduct(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	reason := strings.TrimSpace(r.PostFormValue("revoke_reason"))
	if reason == "" {
		h.renderDetail(w, r, http.StatusBadRequest, dept, product, approvalview.FormInput{},
			map[string]string{"revoke_reason": "取消理由は必須です"}, "")
		return
	}

	q := repository.New(h.db)
	active, err := q.GetActiveApproval(r.Context(), repository.GetActiveApprovalParams{
		DepartmentID: dept.ID,
		ProductID:    product.ID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get active approval for revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var revokedBy *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		revokedBy = sess.AppUserID
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for approval revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := q.WithTx(tx)

	affected, err := qtx.RevokeApproval(r.Context(), repository.RevokeApprovalParams{
		RevokedByAppUserID: revokedBy,
		RevokeReason:       &reason,
		ID:                 active.ID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke approval", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		// GetActiveApproval 通過後に並行取消されたレース。
		_ = tx.Rollback()
		http.NotFound(w, r)
		return
	}
	if err := recordAudit(r.Context(), qtx, r, "approval.revoke", "department_product_approval", active.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for approval revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit approval revoke", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, approvalPath(dept.ID, product.ID), http.StatusSeeOther)
}

// renderGlobal は全社承認設定画面を描画する。
func (h *approvalHandlers) renderGlobal(w http.ResponseWriter, r *http.Request, status int, flash string) {
	q := repository.New(h.db)
	products, err := q.ListProducts(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list products for global approvals", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := approvalview.Global(role, approvalview.GlobalProps{
		Items: products,
		Flash: flash,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render global approvals", "err", err)
	}
}

// globalList は GET /admin/global-approvals。
func (h *approvalHandlers) globalList(w http.ResponseWriter, r *http.Request) {
	h.renderGlobal(w, r, http.StatusOK, "")
}

// globalUpdate は POST /admin/global-approvals/{productID}。
// products.default_approval_status のみを更新し、audit_logs
// (product.default_approval_change) と 1 tx で記録する。
func (h *approvalHandlers) globalUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "productID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	value := strings.TrimSpace(r.PostFormValue("default_approval_status"))
	switch value {
	case approval.DefaultGloballyApproved, approval.DefaultGloballyProhibited,
		approval.DefaultUnknown, approval.DefaultDepartmentDiscretion:
	default:
		h.renderGlobal(w, r, http.StatusBadRequest, "全社設定の値が不正です。")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "begin tx for global approval change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	qtx := repository.New(h.db).WithTx(tx)

	affected, err := qtx.UpdateProductDefaultApprovalStatus(r.Context(), repository.UpdateProductDefaultApprovalStatusParams{
		DefaultApprovalStatus: value,
		ID:                    id,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update default approval status", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		_ = tx.Rollback()
		http.NotFound(w, r)
		return
	}
	if err := recordAudit(r.Context(), qtx, r, "product.default_approval_change", "product", id); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for global approval change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "commit global approval change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/global-approvals", http.StatusSeeOther)
}

// approvalPath は登録・編集画面の URL。
func approvalPath(deptID, productID int64) string {
	return "/approvals/" + strconv.FormatInt(deptID, 10) + "/" + strconv.FormatInt(productID, 10)
}
