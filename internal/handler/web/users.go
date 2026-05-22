package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	userview "github.com/tagawa0525/app_man/internal/view/users"
)

// userHandlers は社員系ハンドラ (List / NewForm / Create / Show /
// EditForm / Update / SoftDelete / Restore) を束ねる。本 PR (PR-D) の
// GREEN サイクル中はメソッドを段階的に追加していく。
type userHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// list は GET /users の一覧 + 検索を返す。検索は employee_code /
// username / name / email の 4 カラム OR LIKE。既定では在籍中
// (deactivated_at IS NULL) のみ。?include_inactive=1 で退職者も含める。
// 検索 ?q= と組合せ可能。
func (h *userHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	includeInactive := r.URL.Query().Get("include_inactive") == "1"

	var (
		users []repository.User
		err   error
	)
	switch {
	case query != "" && includeInactive:
		users, err = q.SearchUsersIncludingInactive(r.Context(), likePattern(query))
	case query != "":
		users, err = q.SearchUsers(r.Context(), likePattern(query))
	case includeInactive:
		users, err = q.ListUsersIncludingInactive(r.Context())
	default:
		users, err = q.ListUsers(r.Context())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	items, err := buildUserListItems(r, q, users)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve departments for users list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	truncated := len(users) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := userview.List(role, query, includeInactive, items, truncated).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render users list", "err", err)
	}
}

// newForm は GET /users/new の新規作成フォームを返す。department_id の
// select 用に現役部署を取得して渡す。
func (h *userHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for new user form", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, http.StatusOK, userview.FormProps{
		Action:      "/users",
		Title:       "ユーザ新規作成",
		Submit:      "作成",
		Departments: depts,
	})
}

func (h *userHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props userview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	role := middleware.RoleFrom(r.Context())
	if err := userview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render users form", "err", err)
	}
}

// create は POST /users の新規作成。検証エラー時は 400/409 で
// 同じフォームを再描画。成功時は 303 で /users/:id へ。
func (h *userHandlers) create(w http.ResponseWriter, r *http.Request) {
	in, parsed, errs := decodeUserForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		depts, perr := q.ListActiveDepartments(r.Context())
		if perr != nil {
			h.logger.ErrorContext(r.Context(), "list departments on validation error", "err", perr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.renderForm(w, r, http.StatusBadRequest, userview.FormProps{
			Action:      "/users",
			Title:       "ユーザ新規作成",
			Submit:      "作成",
			Input:       in,
			Errors:      errs,
			Departments: depts,
		})
		return
	}

	u, err := q.CreateUser(r.Context(), repository.CreateUserParams{
		EmployeeCode: parsed.EmployeeCode,
		Username:     parsed.Username,
		Name:         parsed.Name,
		Email:        parsed.Email,
		DepartmentID: parsed.DepartmentID,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			depts, perr := q.ListActiveDepartments(r.Context())
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusConflict, userview.FormProps{
				Action:      "/users",
				Title:       "ユーザ新規作成",
				Submit:      "作成",
				Input:       in,
				Errors:      map[string]string{"employee_code": "従業員コードが重複しています"},
				Departments: depts,
			})
			return
		}
		if isForeignKeyErr(err) {
			depts, perr := q.ListActiveDepartments(r.Context())
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusBadRequest, userview.FormProps{
				Action:      "/users",
				Title:       "ユーザ新規作成",
				Submit:      "作成",
				Input:       in,
				Errors:      map[string]string{"department_id": "指定された部署は存在しません"},
				Departments: depts,
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "create user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/users/"+strconv.FormatInt(u.ID, 10), http.StatusSeeOther)
}

// show は GET /users/:id の詳細を返す。所属部署があれば lookup して
// テンプレに渡す。
func (h *userHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	u, err := q.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	dept, perr := lookupDepartmentForUser(r, q, u.DepartmentID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup department for user", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := userview.Show(role, userview.ShowProps{User: u, Department: dept}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render user show", "err", err)
	}
}

// editForm は GET /users/:id/edit の編集フォームを返す。
func (h *userHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	u, err := q.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get user for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list active departments for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinned, err := resolvePinnedDepartment(r, q, depts, u.DepartmentID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve pinned department", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, userview.FormProps{
		Action:       "/users/" + strconv.FormatInt(u.ID, 10),
		Title:        "ユーザ編集",
		Submit:       "更新",
		Input:        formInputFromUser(u),
		Departments:  depts,
		PinnedOption: pinned,
	})
}

// update は POST /users/:id の更新。
func (h *userHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	in, parsed, errs := decodeUserForm(r)
	q := repository.New(h.db)

	if len(errs) > 0 {
		depts, perr := q.ListActiveDepartments(r.Context())
		if perr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		pinned, pperr := resolvePinnedDepartment(r, q, depts, parsed.DepartmentID)
		if pperr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.renderForm(w, r, http.StatusBadRequest, userview.FormProps{
			Action:       "/users/" + strconv.FormatInt(id, 10),
			Title:        "ユーザ編集",
			Submit:       "更新",
			Input:        in,
			Errors:       errs,
			Departments:  depts,
			PinnedOption: pinned,
		})
		return
	}

	if _, err := q.UpdateUser(r.Context(), repository.UpdateUserParams{
		EmployeeCode: parsed.EmployeeCode,
		Username:     parsed.Username,
		Name:         parsed.Name,
		Email:        parsed.Email,
		DepartmentID: parsed.DepartmentID,
		ID:           id,
	}); err != nil {
		if isUniqueConstraintErr(err) {
			depts, perr := q.ListActiveDepartments(r.Context())
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			pinned, pperr := resolvePinnedDepartment(r, q, depts, parsed.DepartmentID)
			if pperr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusConflict, userview.FormProps{
				Action:       "/users/" + strconv.FormatInt(id, 10),
				Title:        "ユーザ編集",
				Submit:       "更新",
				Input:        in,
				Errors:       map[string]string{"employee_code": "従業員コードが重複しています"},
				Departments:  depts,
				PinnedOption: pinned,
			})
			return
		}
		if isForeignKeyErr(err) {
			depts, perr := q.ListActiveDepartments(r.Context())
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusBadRequest, userview.FormProps{
				Action:      "/users/" + strconv.FormatInt(id, 10),
				Title:       "ユーザ編集",
				Submit:      "更新",
				Input:       in,
				Errors:      map[string]string{"department_id": "指定された部署は存在しません"},
				Departments: depts,
			})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// delete は POST /users/:id/delete の論理削除 (deactivated_at を立てる)。
// 既に退職扱いなら 409 + flash 付きで show を再描画する。
func (h *userHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.SoftDeleteUser(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "soft delete user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		if _, gerr := q.GetUser(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "このユーザは既に退職扱いです。")
		return
	}
	http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// restore は POST /users/:id/restore の論理削除取り消し
// (deactivated_at を NULL に戻す)。既に在籍中なら 409 + flash 付き再描画。
func (h *userHandlers) restore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.RestoreUser(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "restore user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		if _, gerr := q.GetUser(r.Context(), id); errors.Is(gerr, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.showWithFlash(w, r, id, http.StatusConflict, "このユーザは既に在籍中です。")
		return
	}
	http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// showWithFlash は status を付けて show templ を再描画する (409 用)。
// エラーハンドリングは show ハンドラと同じレベルに揃える。
func (h *userHandlers) showWithFlash(w http.ResponseWriter, r *http.Request, id int64, status int, flash string) {
	q := repository.New(h.db)
	u, err := q.GetUser(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get user for flash", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dept, perr := lookupDepartmentForUser(r, q, u.DepartmentID)
	if perr != nil {
		h.logger.ErrorContext(r.Context(), "lookup department for flash", "err", perr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := userview.Show(role, userview.ShowProps{User: u, Department: dept, Flash: flash}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render user show on conflict", "err", err)
	}
}

// userParsed は decodeUserForm がパースした sqlc 用パラメータ。
type userParsed struct {
	EmployeeCode string
	Username     *string
	Name         string
	Email        *string
	DepartmentID *int64
}

// decodeUserForm は POST フォームから入力を取り出し、必須項目と
// 形式を検証する。戻り値は (生入力, 解析済み, エラーマップ)。
func decodeUserForm(r *http.Request) (userview.FormInput, userParsed, map[string]string) {
	_ = r.ParseForm()
	in := userview.FormInput{
		EmployeeCode: strings.TrimSpace(r.PostFormValue("employee_code")),
		Username:     strings.TrimSpace(r.PostFormValue("username")),
		Name:         strings.TrimSpace(r.PostFormValue("name")),
		Email:        strings.TrimSpace(r.PostFormValue("email")),
		DepartmentID: strings.TrimSpace(r.PostFormValue("department_id")),
	}
	errs := map[string]string{}
	if msg := validateAsciiCode("従業員コード", 64, in.EmployeeCode); msg != "" {
		errs["employee_code"] = msg
	}
	if msg := validateUserName(in.Name); msg != "" {
		errs["name"] = msg
	}
	if msg := validateUsername(in.Username); msg != "" {
		errs["username"] = msg
	}
	if msg := validateEmail(in.Email); msg != "" {
		errs["email"] = msg
	}
	deptID, derr := parseInt64Opt(in.DepartmentID)
	if derr != "" {
		errs["department_id"] = derr
	}
	return in, userParsed{
		EmployeeCode: in.EmployeeCode,
		Username:     nilIfEmpty(in.Username),
		Name:         in.Name,
		Email:        nilIfEmpty(in.Email),
		DepartmentID: deptID,
	}, errs
}

// formInputFromUser は既存レコードを編集フォーム入力値に詰め直す。
func formInputFromUser(u repository.User) userview.FormInput {
	out := userview.FormInput{
		EmployeeCode: u.EmployeeCode,
		Name:         u.Name,
	}
	if u.Username != nil {
		out.Username = *u.Username
	}
	if u.Email != nil {
		out.Email = *u.Email
	}
	if u.DepartmentID != nil {
		out.DepartmentID = strconv.FormatInt(*u.DepartmentID, 10)
	}
	return out
}

// lookupDepartmentForUser は *int64 が nil なら nil を返し、そうでなければ
// id で fetch する。sql.ErrNoRows は nil 扱い (整合性が崩れていても
// show 画面は描画したい)。departments の lookupDepartment と同型を複製
// (3 回ルール未達のため共通化はしない)。
func lookupDepartmentForUser(r *http.Request, q *repository.Queries, id *int64) (*repository.Department, error) {
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

// validateUsername は username フィールドの検証 (任意、AD sAMAccountName 想定)。
func validateUsername(s string) string {
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) > 128 {
		return "ログオン名は 128 文字以内で入力してください"
	}
	return ""
}

// validateUserName は name フィールドの検証 (必須)。
func validateUserName(s string) string {
	if s == "" {
		return "氏名は必須です"
	}
	if utf8.RuneCountInString(s) > 128 {
		return "氏名は 128 文字以内で入力してください"
	}
	return ""
}

// validateEmail は email フィールドの検証 (任意、形式は緩く @ 含むのみ)。
// 仕様書に厳格な形式規定は無いため、見るからにメールでないものだけ弾く。
func validateEmail(s string) string {
	if s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) > 256 {
		return "メールアドレスは 256 文字以内で入力してください"
	}
	if !strings.Contains(s, "@") {
		return "メールアドレスの形式が正しくありません"
	}
	return ""
}

// buildUserListItems は users スライスに対応する所属部署を解決して
// list templ が要求する ListItem に詰め替える。同じ department_id は
// 再 fetch を避けるためキャッシュする。
func buildUserListItems(r *http.Request, q *repository.Queries, users []repository.User) ([]userview.ListItem, error) {
	cache := make(map[int64]*repository.Department)
	out := make([]userview.ListItem, 0, len(users))
	for _, u := range users {
		var dept *repository.Department
		if u.DepartmentID != nil {
			id := *u.DepartmentID
			if v, ok := cache[id]; ok {
				dept = v
			} else {
				d, err := q.GetDepartment(r.Context(), id)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						cache[id] = nil
					} else {
						return nil, err
					}
				} else {
					dept = &d
					cache[id] = dept
				}
			}
		}
		out = append(out, userview.ListItem{User: u, Department: dept})
	}
	return out, nil
}
