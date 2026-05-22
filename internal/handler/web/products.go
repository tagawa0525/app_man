package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	productview "github.com/tagawa0525/app_man/internal/view/products"
)

type productHandlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// list は GET /products の一覧 + 検索。検索は ?q=foo で
// canonical_name / vendor.name / alias_name の LIKE OR 部分一致。
func (h *productHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		items []repository.ListProductsRow
		err   error
	)
	if query != "" {
		searched, serr := q.SearchProducts(r.Context(), likePattern(query))
		if serr != nil {
			h.logger.ErrorContext(r.Context(), "search products", "err", serr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// SearchProductsRow と ListProductsRow は同じカラム集合なので
		// 表示用に詰め替える (templ 側は ListProductsRow を期待)。
		items = make([]repository.ListProductsRow, len(searched))
		for i, s := range searched {
			items[i] = repository.ListProductsRow(s)
		}
	} else {
		items, err = q.ListProducts(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list products", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	truncated := len(items) >= listLimit
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := productview.List(role, query, items, truncated).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render products list", "err", err)
	}
}

// newForm は GET /products/new。vendor select を埋めるため vendors を取得。
func (h *productHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	vs, err := q.ListVendors(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list vendors for new product", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, http.StatusOK, productview.FormProps{
		Action:  "/products",
		Title:   "製品新規作成",
		Submit:  "作成",
		Vendors: vs,
		Input: productview.FormInput{
			SoftwareType:          "installed",
			DefaultApprovalStatus: "unknown",
		},
	})
}

func (h *productHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props productview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	role := middleware.RoleFrom(r.Context())
	if err := productview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render products form", "err", err)
	}
}

// show は GET /products/:id。alias 一覧を併記。
func (h *productHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	p, err := q.GetProduct(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get product", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	aliases, err := q.ListAliasesByProduct(r.Context(), p.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list aliases", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := productview.Show(role, productview.ShowProps{
		Product: p,
		Aliases: aliases,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render product show", "err", err)
	}
}

