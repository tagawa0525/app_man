package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
