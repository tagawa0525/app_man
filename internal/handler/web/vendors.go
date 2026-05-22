package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	vendorview "github.com/tagawa0525/app_man/internal/view/vendors"
)

// vendorHandlers はベンダー系ハンドラ (List / NewForm / Create / Show /
// EditForm / Update / Delete) を束ねる。
type vendorHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

const listLimit = 200

// list は GET /vendors の一覧 + 検索を返す。検索クエリ ?q=foo は LIKE
// 部分一致で適用、未指定なら全件 (200 件まで)。
func (h *vendorHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		items []repository.Vendor
		err   error
	)
	if query != "" {
		items, err = q.SearchVendors(r.Context(), likePattern(query))
	} else {
		items, err = q.ListVendors(r.Context())
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list vendors", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	truncated := len(items) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := vendorview.List(role, query, items, truncated).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render vendors list", "err", err)
	}
}

// newForm は GET /vendors/new の新規作成フォームを返す。
func (h *vendorHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, r, vendorview.FormProps{
		Action: "/vendors",
		Title:  "ベンダー新規作成",
		Submit: "作成",
	})
}

func (h *vendorHandlers) renderForm(w http.ResponseWriter, r *http.Request, props vendorview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	role := middleware.RoleFrom(r.Context())
	if err := vendorview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render vendors form", "err", err)
	}
}

// likePattern はユーザ入力を LIKE 用にエスケープし %term% を付与する。
// SQLite の LIKE は % と _ が wildcard なので、これらを含む検索語をそのまま
// 渡すと意図しない巨大マッチになる。ESCAPE 句は使わず Go 側で除去する
// (要件: 部分一致で十分、正確な % 検索は不要)。
func likePattern(q string) string {
	cleaned := strings.NewReplacer("%", "", "_", "", "\\", "").Replace(q)
	return "%" + cleaned + "%"
}

// isUniqueConstraintErr は sqlite の SQLITE_CONSTRAINT_UNIQUE (2067) か判定する。
func isUniqueConstraintErr(err error) bool {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	return serr.Code() == 2067
}

// parseInt64Param は chi の URLParam("id" 等) を int64 化する。
// 失敗時は ok=false を返し、呼び出し元で 400 を出す。
func parseInt64Param(r *http.Request, name string) (int64, bool) {
	raw := chi.URLParam(r, name)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// nilIfEmpty は string が空なら *string=nil、そうでなければポインタを返す。
// sqlc の nullable column 引数 (*string) を作るのに使う。
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

