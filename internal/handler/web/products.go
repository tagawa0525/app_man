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

// editForm は GET /products/:id/edit。
func (h *productHandlers) editForm(w http.ResponseWriter, r *http.Request) {
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
		h.logger.ErrorContext(r.Context(), "get product for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	vs, err := q.ListVendors(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list vendors for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, productview.FormProps{
		Action:  "/products/" + strconv.FormatInt(p.ID, 10),
		Title:   "製品編集",
		Submit:  "更新",
		Vendors: vs,
		Input:   productRowToInput(p),
	})
}

// create は POST /products。
func (h *productHandlers) create(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	vs, err := q.ListVendors(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list vendors for create", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	input, errs := decodeProductForm(r, vs)
	formProps := productview.FormProps{
		Action:  "/products",
		Title:   "製品新規作成",
		Submit:  "作成",
		Vendors: vs,
		Input:   input.toViewInput(),
		Errors:  errs,
	}
	if len(errs) > 0 {
		h.renderForm(w, r, http.StatusBadRequest, formProps)
		return
	}

	p, err := q.CreateProduct(r.Context(), input.toCreateParams())
	if err != nil {
		if isUniqueConstraintErr(err) {
			formProps.Errors = map[string]string{
				"canonical_name": "同じベンダー・名前・エディションの製品が既に存在します",
			}
			h.renderForm(w, r, http.StatusConflict, formProps)
			return
		}
		h.logger.ErrorContext(r.Context(), "create product", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/products/"+strconv.FormatInt(p.ID, 10), http.StatusSeeOther)
}

// update は POST /products/:id。
func (h *productHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	vs, err := q.ListVendors(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	input, errs := decodeProductForm(r, vs)
	formProps := productview.FormProps{
		Action:  "/products/" + strconv.FormatInt(id, 10),
		Title:   "製品編集",
		Submit:  "更新",
		Vendors: vs,
		Input:   input.toViewInput(),
		Errors:  errs,
	}
	if len(errs) > 0 {
		h.renderForm(w, r, http.StatusBadRequest, formProps)
		return
	}

	if _, err := q.UpdateProduct(r.Context(), input.toUpdateParams(id)); err != nil {
		if isUniqueConstraintErr(err) {
			formProps.Errors = map[string]string{
				"canonical_name": "同じベンダー・名前・エディションの製品が既に存在します",
			}
			h.renderForm(w, r, http.StatusConflict, formProps)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update product", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/products/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// aliasCreate は POST /products/:id/aliases。
func (h *productHandlers) aliasCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostFormValue("alias_name"))
	if name == "" {
		http.Redirect(w, r, "/products/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}

	q := repository.New(h.db)
	if _, err := q.CreateAlias(r.Context(), repository.CreateAliasParams{
		ProductID: id,
		AliasName: name,
	}); err != nil {
		if isUniqueConstraintErr(err) {
			// product_aliases.alias_name は GLOBAL UNIQUE。
			// 既存があれば 409 + show 画面を flash 付きで再表示する。
			h.showWithFlash(w, r, id, http.StatusConflict, "同じエイリアスが既に存在します。別の名前を使ってください。")
			return
		}
		h.logger.ErrorContext(r.Context(), "create alias", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/products/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// aliasDelete は POST /products/:id/aliases/:aid/delete。
// URL の {id} (product) と {aid} (alias) が一致しなければ何も削除せず 404。
// 別製品の alias の ID を推測して URL を組み立てても削除できないようにする
// ため、DeleteAlias は WHERE id = ? AND product_id = ? で守る。
func (h *productHandlers) aliasDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	aid, ok := parseInt64Param(r, "aid")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	affected, err := q.DeleteAlias(r.Context(), repository.DeleteAliasParams{
		ID:        aid,
		ProductID: id,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete alias", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/products/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// showWithFlash は status を付けて show templ を再描画する (409 などで使う)。
func (h *productHandlers) showWithFlash(w http.ResponseWriter, r *http.Request, id int64, status int, flash string) {
	q := repository.New(h.db)
	p, err := q.GetProduct(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	aliases, err := q.ListAliasesByProduct(r.Context(), p.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := productview.Show(role, productview.ShowProps{
		Product: p,
		Aliases: aliases,
		Flash:   flash,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render product show on conflict", "err", err)
	}
}

// delete は POST /products/:id/delete。
func (h *productHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	if err := q.DeleteProduct(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete product", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/products", http.StatusSeeOther)
}

// productInput は decodeProductForm が返すフォーム入力値 (view 用構造体と
// CreateProductParams / UpdateProductParams の橋渡しを兼ねる)。
type productInput struct {
	VendorID              int64
	CanonicalName         string
	Edition               string
	SoftwareType          string
	LicenseRequired       *bool
	DefaultApprovalStatus string
	CanonicalDownloadURL  string
	ServiceAdminURL       string
	LicenseTermsURL       string
	Note                  string
}

func (in productInput) toViewInput() productview.FormInput {
	licReq := ""
	if in.LicenseRequired != nil {
		if *in.LicenseRequired {
			licReq = "true"
		} else {
			licReq = "false"
		}
	}
	vendorID := ""
	if in.VendorID > 0 {
		vendorID = strconv.FormatInt(in.VendorID, 10)
	}
	return productview.FormInput{
		VendorID:              vendorID,
		CanonicalName:         in.CanonicalName,
		Edition:               in.Edition,
		SoftwareType:          in.SoftwareType,
		LicenseRequired:       licReq,
		DefaultApprovalStatus: in.DefaultApprovalStatus,
		CanonicalDownloadURL:  in.CanonicalDownloadURL,
		ServiceAdminURL:       in.ServiceAdminURL,
		LicenseTermsURL:       in.LicenseTermsURL,
		Note:                  in.Note,
	}
}

func (in productInput) toCreateParams() repository.CreateProductParams {
	return repository.CreateProductParams{
		VendorID:              in.VendorID,
		CanonicalName:         in.CanonicalName,
		Edition:               nilIfEmpty(in.Edition),
		SoftwareType:          in.SoftwareType,
		LicenseRequired:       in.LicenseRequired,
		DefaultApprovalStatus: in.DefaultApprovalStatus,
		CanonicalDownloadUrl:  nilIfEmpty(in.CanonicalDownloadURL),
		ServiceAdminUrl:       nilIfEmpty(in.ServiceAdminURL),
		LicenseTermsUrl:       nilIfEmpty(in.LicenseTermsURL),
		Note:                  nilIfEmpty(in.Note),
	}
}

func (in productInput) toUpdateParams(id int64) repository.UpdateProductParams {
	return repository.UpdateProductParams{
		VendorID:              in.VendorID,
		CanonicalName:         in.CanonicalName,
		Edition:               nilIfEmpty(in.Edition),
		SoftwareType:          in.SoftwareType,
		LicenseRequired:       in.LicenseRequired,
		DefaultApprovalStatus: in.DefaultApprovalStatus,
		CanonicalDownloadUrl:  nilIfEmpty(in.CanonicalDownloadURL),
		ServiceAdminUrl:       nilIfEmpty(in.ServiceAdminURL),
		LicenseTermsUrl:       nilIfEmpty(in.LicenseTermsURL),
		Note:                  nilIfEmpty(in.Note),
		ID:                    id,
	}
}

// productRowToInput は GetProductRow から view 用 Input へ詰め替え。
func productRowToInput(p repository.GetProductRow) productview.FormInput {
	in := productInput{
		VendorID:              p.VendorID,
		CanonicalName:         p.CanonicalName,
		Edition:               derefString(p.Edition),
		SoftwareType:          p.SoftwareType,
		LicenseRequired:       p.LicenseRequired,
		DefaultApprovalStatus: p.DefaultApprovalStatus,
		CanonicalDownloadURL:  derefString(p.CanonicalDownloadUrl),
		ServiceAdminURL:       derefString(p.ServiceAdminUrl),
		LicenseTermsURL:       derefString(p.LicenseTermsUrl),
		Note:                  derefString(p.Note),
	}
	return in.toViewInput()
}

// decodeProductForm はフォーム入力を取り出し、enum 含めて検証する。
func decodeProductForm(r *http.Request, vendors []repository.Vendor) (productInput, map[string]string) {
	_ = r.ParseForm()
	rawVendorID := strings.TrimSpace(r.PostFormValue("vendor_id"))
	canonical := strings.TrimSpace(r.PostFormValue("canonical_name"))
	edition := strings.TrimSpace(r.PostFormValue("edition"))
	softwareType := strings.TrimSpace(r.PostFormValue("software_type"))
	if softwareType == "" {
		softwareType = "installed"
	}
	licReq := strings.TrimSpace(r.PostFormValue("license_required"))
	approval := strings.TrimSpace(r.PostFormValue("default_approval_status"))
	if approval == "" {
		approval = "unknown"
	}
	downloadURL := strings.TrimSpace(r.PostFormValue("canonical_download_url"))
	adminURL := strings.TrimSpace(r.PostFormValue("service_admin_url"))
	termsURL := strings.TrimSpace(r.PostFormValue("license_terms_url"))
	note := r.PostFormValue("note")

	errs := map[string]string{}

	var vendorID int64
	if rawVendorID == "" {
		errs["vendor_id"] = "ベンダーを選択してください"
	} else {
		parsed, err := strconv.ParseInt(rawVendorID, 10, 64)
		if err != nil {
			errs["vendor_id"] = "不正なベンダー ID です"
		} else if !vendorExists(vendors, parsed) {
			errs["vendor_id"] = "ベンダーが存在しません"
		} else {
			vendorID = parsed
		}
	}

	if canonical == "" {
		errs["canonical_name"] = "製品名は必須です"
	}

	switch softwareType {
	case "installed", "saas", "both":
	default:
		errs["software_type"] = "不正な種別です"
	}

	switch approval {
	case "globally_approved", "globally_prohibited", "department_discretion", "unknown":
	default:
		errs["default_approval_status"] = "不正な承認状態です"
	}

	var licReqPtr *bool
	switch licReq {
	case "":
		// 未判定
	case "true":
		t := true
		licReqPtr = &t
	case "false":
		f := false
		licReqPtr = &f
	default:
		errs["license_required"] = "不正な値です"
	}

	if msg := validateHTTPURL(downloadURL); msg != "" {
		errs["canonical_download_url"] = msg
	}
	if msg := validateHTTPURL(adminURL); msg != "" {
		errs["service_admin_url"] = msg
	}
	if msg := validateHTTPURL(termsURL); msg != "" {
		errs["license_terms_url"] = msg
	}

	in := productInput{
		VendorID:              vendorID,
		CanonicalName:         canonical,
		Edition:               edition,
		SoftwareType:          softwareType,
		LicenseRequired:       licReqPtr,
		DefaultApprovalStatus: approval,
		CanonicalDownloadURL:  downloadURL,
		ServiceAdminURL:       adminURL,
		LicenseTermsURL:       termsURL,
		Note:                  note,
	}
	return in, errs
}

func vendorExists(vs []repository.Vendor, id int64) bool {
	for _, v := range vs {
		if v.ID == id {
			return true
		}
	}
	return false
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
