package web

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strings"

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
// 部分一致 (LIKE)。既定では現役部署 (valid_to IS NULL) のみを返し、
// ?include_inactive=1 で廃止済みも含める (このコミットでは未実装、
// 後続コミットで分岐を入れる)。
func (h *departmentHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		items []repository.Department
		err   error
	)
	if query != "" {
		items, err = q.SearchDepartments(r.Context(), likePattern(query))
	} else {
		items, err = q.ListDepartments(r.Context())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list departments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	truncated := len(items) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := departmentview.List(role, query, items, truncated).Render(r.Context(), w); err != nil {
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
	h.renderForm(w, r, departmentview.FormProps{
		Action:  "/departments",
		Title:   "部署新規作成",
		Submit:  "作成",
		Parents: parents,
	})
}

func (h *departmentHandlers) renderForm(w http.ResponseWriter, r *http.Request, props departmentview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	role := middleware.RoleFrom(r.Context())
	if err := departmentview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render departments form", "err", err)
	}
}
