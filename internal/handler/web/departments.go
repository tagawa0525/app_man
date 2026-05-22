package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	departmentview "github.com/tagawa0525/app_man/internal/view/departments"
)

// departmentHandlers は部署系ハンドラ (List / NewForm / Create / Show /
// EditForm / Update / SoftDelete / Restore) を束ねる。
type departmentHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// list は GET /departments の一覧 + 検索を返す。検索は name / code への
// 部分一致 (LIKE)。既定では現役部署 (valid_to IS NULL) のみ。
// ?include_inactive=1 で廃止済みも含める。検索 ?q= と組合せ可能。
func (h *departmentHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	includeInactive := r.URL.Query().Get("include_inactive") == "1"

	var (
		items []repository.Department
		err   error
	)
	switch {
	case query != "" && includeInactive:
		items, err = q.SearchDepartmentsIncludingInactive(r.Context(), likePattern(query))
	case query != "":
		items, err = q.SearchDepartments(r.Context(), likePattern(query))
	case includeInactive:
		items, err = q.ListDepartmentsIncludingInactive(r.Context())
	default:
		items, err = q.ListDepartments(r.Context())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list departments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	truncated := len(items) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := departmentview.List(role, query, includeInactive, items, truncated).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render departments list", "err", err)
	}
}

// newForm は GET /departments/new の新規作成フォームを返す。parent_id /
// successor_department_id の select 用に現役部署を取得して渡す。
func (h *departmentHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	parents, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for new form", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, http.StatusOK, departmentview.FormProps{
		Action:  "/departments",
		Title:   "部署新規作成",
		Submit:  "作成",
		Parents: parents,
	})
}

func (h *departmentHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props departmentview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	role := middleware.RoleFrom(r.Context())
	if err := departmentview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render departments form", "err", err)
	}
}

// create は POST /departments の新規作成。検証エラー時は 400/409 で
// 同じフォームを再描画。成功時は 303 で /departments/:id へ。
func (h *departmentHandlers) create(w http.ResponseWriter, r *http.Request) {
	in, parsed, errs := decodeDepartmentForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		parents, perr := q.ListActiveDepartments(r.Context())
		if perr != nil {
			h.logger.ErrorContext(r.Context(), "list active departments on validation error", "err", perr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.renderForm(w, r, http.StatusBadRequest, departmentview.FormProps{
			Action:  "/departments",
			Title:   "部署新規作成",
			Submit:  "作成",
			Input:   in,
			Errors:  errs,
			Parents: parents,
		})
		return
	}

	d, err := q.CreateDepartment(r.Context(), repository.CreateDepartmentParams{
		Code:                  parsed.Code,
		Name:                  parsed.Name,
		ParentID:              parsed.ParentID,
		SuccessorDepartmentID: parsed.SuccessorID,
		ValidFrom:             parsed.ValidFrom,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			parents, perr := q.ListActiveDepartments(r.Context())
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			errs := map[string]string{"code": "部署コードが重複しています"}
			h.renderForm(w, r, http.StatusConflict, departmentview.FormProps{
				Action:  "/departments",
				Title:   "部署新規作成",
				Submit:  "作成",
				Input:   in,
				Errors:  errs,
				Parents: parents,
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "create department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/departments/"+strconv.FormatInt(d.ID, 10), http.StatusSeeOther)
}

// show は GET /departments/:id の詳細 + 子部署一覧 + 親 / 後継部署リンクを返す。
func (h *departmentHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	d, err := q.GetDepartment(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	children, err := q.ListChildDepartments(r.Context(), &id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list child departments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	props := departmentview.ShowProps{Department: d, Children: children}
	if parent, perr := lookupDepartment(r, q, d.ParentID); perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup parent department", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else {
		props.Parent = parent
	}
	if successor, serr := lookupDepartment(r, q, d.SuccessorDepartmentID); serr != nil {
		h.logger.ErrorContext(r.Context(), "lookup successor department", "err", serr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else {
		props.Successor = successor
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := departmentview.Show(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render department show", "err", err)
	}
}

// editForm は GET /departments/:id/edit の編集フォームを返す。
func (h *departmentHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	d, err := q.GetDepartment(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get department for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	parents, err := q.ListActiveDepartmentsExceptID(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments except id", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, departmentview.FormProps{
		Action:  "/departments/" + strconv.FormatInt(d.ID, 10),
		Title:   "部署編集",
		Submit:  "更新",
		Input:   formInputFromDepartment(d),
		Parents: parents,
	})
}

// update は POST /departments/:id の更新。
func (h *departmentHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	in, parsed, errs := decodeDepartmentForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		parents, perr := q.ListActiveDepartmentsExceptID(r.Context(), id)
		if perr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.renderForm(w, r, http.StatusBadRequest, departmentview.FormProps{
			Action:  "/departments/" + strconv.FormatInt(id, 10),
			Title:   "部署編集",
			Submit:  "更新",
			Input:   in,
			Errors:  errs,
			Parents: parents,
		})
		return
	}

	if _, err := q.UpdateDepartment(r.Context(), repository.UpdateDepartmentParams{
		Code:                  parsed.Code,
		Name:                  parsed.Name,
		ParentID:              parsed.ParentID,
		SuccessorDepartmentID: parsed.SuccessorID,
		ValidFrom:             parsed.ValidFrom,
		ID:                    id,
	}); err != nil {
		if isUniqueConstraintErr(err) {
			parents, perr := q.ListActiveDepartmentsExceptID(r.Context(), id)
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusConflict, departmentview.FormProps{
				Action:  "/departments/" + strconv.FormatInt(id, 10),
				Title:   "部署編集",
				Submit:  "更新",
				Input:   in,
				Errors:  map[string]string{"code": "部署コードが重複しています"},
				Parents: parents,
			})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/departments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// delete は POST /departments/:id/delete の論理削除 (valid_to を立てる)。
// 既に廃止済みなら 409 + flash 付きで show を再描画する。
func (h *departmentHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.SoftDeleteDepartment(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "soft delete department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		// id が存在しないなら 404、廃止済みなら 409。
		if _, gerr := q.GetDepartment(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "この部署は既に廃止されています。")
		return
	}
	http.Redirect(w, r, "/departments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// restore は POST /departments/:id/restore の論理削除取り消し
// (valid_to を NULL に戻す)。既に現役なら 409 + flash 付き再描画。
func (h *departmentHandlers) restore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.RestoreDepartment(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "restore department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		if _, gerr := q.GetDepartment(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "この部署は既に現役です。")
		return
	}
	http.Redirect(w, r, "/departments/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// showWithFlash は status を付けて show templ を再描画する (409 用)。
func (h *departmentHandlers) showWithFlash(w http.ResponseWriter, r *http.Request, id int64, status int, flash string) {
	q := repository.New(h.db)
	d, err := q.GetDepartment(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	children, err := q.ListChildDepartments(r.Context(), &id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	props := departmentview.ShowProps{Department: d, Children: children, Flash: flash}
	if parent, perr := lookupDepartment(r, q, d.ParentID); perr == nil {
		props.Parent = parent
	}
	if successor, serr := lookupDepartment(r, q, d.SuccessorDepartmentID); serr == nil {
		props.Successor = successor
	}
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := departmentview.Show(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render department show on conflict", "err", err)
	}
}

// departmentParsed は decodeDepartmentForm がパースした sqlc 用パラメータ。
type departmentParsed struct {
	Code        string
	Name        string
	ParentID    *int64
	SuccessorID *int64
	ValidFrom   *time.Time
}

// decodeDepartmentForm は POST フォームから入力を取り出し、必須項目と
// 形式を検証する。戻り値は (生入力, 解析済み, エラーマップ)。
func decodeDepartmentForm(r *http.Request) (departmentview.FormInput, departmentParsed, map[string]string) {
	_ = r.ParseForm()
	in := departmentview.FormInput{
		Code:        strings.TrimSpace(r.PostFormValue("code")),
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		ParentID:    strings.TrimSpace(r.PostFormValue("parent_id")),
		SuccessorID: strings.TrimSpace(r.PostFormValue("successor_department_id")),
		ValidFrom:   strings.TrimSpace(r.PostFormValue("valid_from")),
	}
	errs := map[string]string{}
	if msg := validateDepartmentCode(in.Code); msg != "" {
		errs["code"] = msg
	}
	if msg := validateDepartmentName(in.Name); msg != "" {
		errs["name"] = msg
	}
	parentID, perr := parseInt64Opt(in.ParentID)
	if perr != "" {
		errs["parent_id"] = perr
	}
	successorID, serr := parseInt64Opt(in.SuccessorID)
	if serr != "" {
		errs["successor_department_id"] = serr
	}
	validFrom, ferr := parseDateOpt(in.ValidFrom)
	if ferr != "" {
		errs["valid_from"] = ferr
	}
	return in, departmentParsed{
		Code: in.Code, Name: in.Name,
		ParentID: parentID, SuccessorID: successorID, ValidFrom: validFrom,
	}, errs
}

// formInputFromDepartment は既存レコードを編集フォーム入力値に詰め直す。
func formInputFromDepartment(d repository.Department) departmentview.FormInput {
	out := departmentview.FormInput{
		Code: d.Code,
		Name: d.Name,
	}
	if d.ParentID != nil {
		out.ParentID = strconv.FormatInt(*d.ParentID, 10)
	}
	if d.SuccessorDepartmentID != nil {
		out.SuccessorID = strconv.FormatInt(*d.SuccessorDepartmentID, 10)
	}
	if d.ValidFrom != nil {
		out.ValidFrom = d.ValidFrom.Format("2006-01-02")
	}
	return out
}

// lookupDepartment は *int64 が nil なら nil を返し、そうでなければ id で fetch する。
// sql.ErrNoRows は nil 扱い (整合性が崩れていても show 画面は描画したい)。
func lookupDepartment(r *http.Request, q *repository.Queries, id *int64) (*repository.Department, error) {
	if id == nil {
		return nil, nil
	}
	d, err := q.GetDepartment(r.Context(), *id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// deptCodeRe は部署コードの受け付け可能文字。要件書 §10 で AD 連携キーと
// される code は基本的に英数 + 区切り記号で構成される想定。
var deptCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateDepartmentCode は code フィールドの検証。
func validateDepartmentCode(s string) string {
	if s == "" {
		return "部署コードは必須です"
	}
	if utf8.RuneCountInString(s) > 64 {
		return "部署コードは 64 文字以内で入力してください"
	}
	if !deptCodeRe.MatchString(s) {
		return "部署コードは英数・ハイフン・アンダースコアで入力してください"
	}
	return ""
}

// validateDepartmentName は name フィールドの検証。
func validateDepartmentName(s string) string {
	if s == "" {
		return "名称は必須です"
	}
	if utf8.RuneCountInString(s) > 128 {
		return "名称は 128 文字以内で入力してください"
	}
	return ""
}

// parseInt64Opt は string を *int64 にする。空文字は nil を返す。
// 形式不正やゼロ以下は (nil, エラーメッセージ) を返す。
func parseInt64Opt(s string) (*int64, string) {
	if s == "" {
		return nil, ""
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return nil, "値が不正です"
	}
	return &v, ""
}

// parseDateOpt は string を *time.Time にする。空文字は nil を返す。
// 期待形式は YYYY-MM-DD。
func parseDateOpt(s string) (*time.Time, string) {
	if s == "" {
		return nil, ""
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, "日付は YYYY-MM-DD 形式で入力してください"
	}
	return &t, ""
}
