package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
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
	h.renderForm(w, r, http.StatusOK, vendorview.FormProps{
		Action: "/vendors",
		Title:  "ベンダー新規作成",
		Submit: "作成",
	})
}

// create は POST /vendors の新規作成。検証エラー時は 400/409 で
// 同じフォームを再表示する。成功時は 303 で /vendors/:id へ。
func (h *vendorHandlers) create(w http.ResponseWriter, r *http.Request) {
	input, errs := decodeVendorForm(r)
	if len(errs) > 0 {
		h.renderForm(w, r, http.StatusBadRequest, vendorview.FormProps{
			Action: "/vendors",
			Title:  "ベンダー新規作成",
			Submit: "作成",
			Input:  input,
			Errors: errs,
		})
		return
	}

	q := repository.New(h.db)
	v, err := q.CreateVendor(r.Context(), repository.CreateVendorParams{
		Name: input.Name,
		Url:  nilIfEmpty(input.URL),
		Note: nilIfEmpty(input.Note),
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			h.renderForm(w, r, http.StatusConflict, vendorview.FormProps{
				Action: "/vendors",
				Title:  "ベンダー新規作成",
				Submit: "作成",
				Input:  input,
				Errors: map[string]string{"name": "同じ名前のベンダーが既に存在します"},
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "create vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/vendors/"+strconv.FormatInt(v.ID, 10), http.StatusSeeOther)
}

// show は GET /vendors/:id の詳細 + 配下 product 一覧を返す。
func (h *vendorHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	v, err := q.GetVendor(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	products, err := q.ListProductsByVendor(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list products by vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := vendorview.Show(role, vendorview.ShowProps{
		Vendor:   v,
		Products: products,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render vendor show", "err", err)
	}
}

// editForm は GET /vendors/:id/edit の編集フォームを返す。
func (h *vendorHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	v, err := q.GetVendor(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get vendor for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, vendorview.FormProps{
		Action: "/vendors/" + strconv.FormatInt(v.ID, 10),
		Title:  "ベンダー編集",
		Submit: "更新",
		Input: vendorview.FormInput{
			Name: v.Name,
			URL:  derefString(v.Url),
			Note: derefString(v.Note),
		},
	})
}

// update は POST /vendors/:id の更新。
func (h *vendorHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	input, errs := decodeVendorForm(r)
	formProps := vendorview.FormProps{
		Action: "/vendors/" + strconv.FormatInt(id, 10),
		Title:  "ベンダー編集",
		Submit: "更新",
		Input:  input,
		Errors: errs,
	}
	if len(errs) > 0 {
		h.renderForm(w, r, http.StatusBadRequest, formProps)
		return
	}

	q := repository.New(h.db)
	if _, err := q.UpdateVendor(r.Context(), repository.UpdateVendorParams{
		Name: input.Name,
		Url:  nilIfEmpty(input.URL),
		Note: nilIfEmpty(input.Note),
		ID:   id,
	}); err != nil {
		if isUniqueConstraintErr(err) {
			formProps.Errors = map[string]string{"name": "同じ名前のベンダーが既に存在します"}
			h.renderForm(w, r, http.StatusConflict, formProps)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/vendors/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// delete は POST /vendors/:id/delete。配下に product があれば 409。
func (h *vendorHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	count, err := q.CountProductsByVendor(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count products by vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		v, gerr := q.GetVendor(r.Context(), id)
		if gerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		products, perr := q.ListProductsByVendor(r.Context(), id)
		if perr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		role := middleware.RoleFrom(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		if rerr := vendorview.Show(role, vendorview.ShowProps{
			Vendor:   v,
			Products: products,
			Flash:    "配下に製品が紐づいているため削除できません。先に製品を削除または別ベンダーへ付け替えてください。",
		}).Render(r.Context(), w); rerr != nil {
			h.logger.ErrorContext(r.Context(), "render vendor show on conflict", "err", rerr)
		}
		return
	}

	if err := q.DeleteVendor(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete vendor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/vendors", http.StatusSeeOther)
}

// decodeVendorForm は POST フォームから入力を取り出し、必須項目を検証する。
// 戻り値 errs はフィールド名 → メッセージのマップ。空ならエラー無し。
func decodeVendorForm(r *http.Request) (vendorview.FormInput, map[string]string) {
	_ = r.ParseForm()
	in := vendorview.FormInput{
		Name: strings.TrimSpace(r.PostFormValue("name")),
		URL:  strings.TrimSpace(r.PostFormValue("url")),
		Note: r.PostFormValue("note"),
	}
	errs := map[string]string{}
	if in.Name == "" {
		errs["name"] = "名前は必須です"
	}
	if msg := validateHTTPURL(in.URL); msg != "" {
		errs["url"] = msg
	}
	return in, errs
}

// derefString は *string を string にする (nil なら空文字)。
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (h *vendorHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props vendorview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
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

// isForeignKeyErr は sqlite の SQLITE_CONSTRAINT_FOREIGNKEY (787) か判定する。
func isForeignKeyErr(err error) bool {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	return serr.Code() == 787
}

// parseInt64Param は chi の URLParam("id" 等) を int64 化する。
// パースに失敗 or 0 以下なら ok=false。呼び出し元は http.NotFound で
// 404 を返す (URL に不正な ID が混入したケースは存在しないリソースと
// 同じ扱い)。
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

// validateHTTPURL はユーザ入力 URL を検証し、エラーメッセージを返す。
// 空文字 (省略) は許容して "" を返す。スキームが http / https 以外
// (javascript: / data: / file: 等) は弾く — show templ で href に直接
// 埋め込む箇所があり、`javascript:` を保存できると XSS になるため。
// 相対 URL (host 無し) も弾く (リンク先として意味を成さない)。
func validateHTTPURL(input string) string {
	if input == "" {
		return ""
	}
	u, err := url.Parse(input)
	if err != nil {
		return "URL の形式が不正です"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "URL は http:// または https:// で始めてください"
	}
	if u.Host == "" {
		return "URL のホストが指定されていません"
	}
	return ""
}
