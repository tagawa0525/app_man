package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/filestore"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/slug"
	licenseview "github.com/tagawa0525/app_man/internal/view/licenses"
)

// licenseHandlers はライセンス系ハンドラ (List / Show / NewForm / Create /
// EditForm / Update) を束ねる。削除ルートは提供しない — 契約満了は
// expires_at (論理削除の日時カラム規約) で表現し、過去契約はレコードとして
// 残す (仕様 §5.2)。
type licenseHandlers struct {
	db     *sql.DB
	logger *slog.Logger
	// store / fsCfg は証書ファイルの物理配置 (L-3)。store が保存・オープン、
	// fsCfg.BasePath がディレクトリ作成・rename・meta.yml の書込み先。
	store *filestore.Store
	fsCfg config.FileStoreConfig
}

// expiringSoonDays は一覧で「期限接近」警告を出すしきい値 (90 日)。
const expiringSoonDays = 90

// list は GET /licenses の一覧。デフォルトは現役のみ
// (expires_at IS NULL または未来)、?expired=1 で満了込み。
// 並びは repository 層で期限昇順 (無期限は最後) に揃えてある。
func (h *licenseHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	role := middleware.RoleFrom(r.Context())
	includeExpired := r.URL.Query().Get("expired") == "1"

	flag := int64(0)
	if includeExpired {
		flag = 1
	}
	rows, err := q.ListLicenses(r.Context(), flag)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list licenses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 満了 / 期限接近の表示フラグは、リポジトリ層の現役判定
	// (expires_at >= date('now')、SQLite の date('now') は UTC 日付) と
	// 揃えるため、両判定とも UTC の日付単位で比較する。時刻成分や
	// ローカル TZ を混ぜると SQL の絞り込みと画面表示が食い違う。
	// 満了 = UTC 日付が今日より前。期限接近 = 満了でなく、UTC 日付が
	// 今日から 90 日後まで (ちょうど 90 日後を含む)。
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	soonLimit := today.AddDate(0, 0, expiringSoonDays)
	items := make([]licenseview.ListItem, len(rows))
	for i, row := range rows {
		item := licenseview.ListItem{License: row}
		if row.ExpiresAt != nil {
			exp := row.ExpiresAt.In(time.UTC)
			expDay := time.Date(exp.Year(), exp.Month(), exp.Day(), 0, 0, 0, 0, time.UTC)
			switch {
			case expDay.Before(today):
				item.Expired = true
			case !expDay.After(soonLimit):
				item.ExpiringSoon = true
			}
		}
		items[i] = item
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := licenseview.List(role, includeExpired, items).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render licenses list", "err", err)
	}
}

// show は GET /licenses/:id の詳細。product_keys は平文を画面に出さず
// 「登録あり / なし」のみ表示する (write-only。閲覧 + audit_logs 記録は
// L-3 の責務)。view へは値そのものを渡さない。
func (h *licenseHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.renderShow(w, r, id, http.StatusOK, "")
}

// renderShow は license 詳細 (基本情報 + 割当セクション) を status /
// flash 付きで描画する。割当の追加エラー (400 / 409) の再描画にも使う。
// 超過警告は count_unit に一致する側のアクティブ割当数が total_count を
// 超えたときのみ (NULL = 無制限は警告なし)。超過してもブロックはしない
// (本システムの思想は可視化)。
func (h *licenseHandlers) renderShow(w http.ResponseWriter, r *http.Request, id int64, status int, flash string) {
	h.renderShowKeys(w, r, id, status, flash, "")
}

// renderShowKeys は renderShow の実体。revealedKeys が非空のとき (キー閲覧
// POST の応答のみ) だけ平文キーを view に渡す。呼び出し側で audit_logs への
// INSERT 成功を確認してから渡すこと。
func (h *licenseHandlers) renderShowKeys(w http.ResponseWriter, r *http.Request, id int64, status int, flash, revealedKeys string) {
	q := repository.New(h.db)
	row, err := q.GetLicenseByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasKeys := row.ProductKeys != nil && strings.TrimSpace(*row.ProductKeys) != ""
	row.ProductKeys = nil // 平文を view に渡さない

	userAsgs, err := q.ListActiveUserAssignmentsByLicense(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list user assignments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	deviceAsgs, err := q.ListActiveDeviceAssignmentsByLicense(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list device assignments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	docs, err := q.ListLicenseDocumentsByLicense(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list license documents", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	role := middleware.RoleFrom(r.Context())

	// 割当フォームの選択肢: ユーザ = 在職者のみ、端末 = 現役のみ
	// (退職者・退役端末への新規割当は事故)。フォームは編集ロールにしか
	// 表示しないので、選択肢クエリも編集ロールのときだけ実行する
	// (viewer 等では空スライスのまま)。専用クエリは LIMIT なし —
	// 選択肢から漏れた対象は画面から割当不能になるため、一覧画面向けの
	// LIMIT 200 クエリ (ListActiveUsers / ListDevices) は使わない。
	var (
		activeUsers   []repository.ListActiveUsersForSelectRow
		activeDevices []repository.ListActiveDevicesForSelectRow
	)
	if isEditorRole(role) {
		activeUsers, err = q.ListActiveUsersForSelect(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list active users for select", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		activeDevices, err = q.ListActiveDevicesForSelect(r.Context())
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list active devices for select", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	assigned := int64(len(deviceAsgs))
	if row.CountUnit == "user" {
		assigned = int64(len(userAsgs))
	}
	over := row.TotalCount != nil && assigned > *row.TotalCount
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := licenseview.Show(role, licenseview.ShowProps{
		License:           row,
		HasKeys:           hasKeys,
		RevealedKeys:      revealedKeys,
		Flash:             flash,
		Documents:         docs,
		UserAssignments:   userAsgs,
		DeviceAssignments: deviceAsgs,
		AssignableUsers:   activeUsers,
		AssignableDevices: activeDevices,
		AssignedCount:     assigned,
		OverAllocated:     over,
	}).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render license show", "err", err)
	}
}

// loadLicenseFormRefs は new / create / edit / update が select の選択肢に
// 使う products (vendor JOIN 済み) と現役部署を取得する。廃止部署
// (valid_to NOT NULL) は新規選択不可のため ListActiveDepartments を使う。
func (h *licenseHandlers) loadLicenseFormRefs(r *http.Request, q *repository.Queries) ([]repository.ListProductsRow, []repository.Department, error) {
	products, err := q.ListProducts(r.Context())
	if err != nil {
		return nil, nil, err
	}
	depts, err := q.ListActiveDepartments(r.Context())
	if err != nil {
		return nil, nil, err
	}
	return products, depts, nil
}

// newForm は GET /licenses/new の新規作成フォーム。
func (h *licenseHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	products, depts, err := h.loadLicenseFormRefs(r, q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load refs for new license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderForm(w, r, http.StatusOK, licenseview.FormProps{
		Action:      "/licenses",
		Title:       "ライセンス新規作成",
		Submit:      "作成",
		Products:    products,
		Departments: depts,
		Input: licenseview.FormInput{
			CountUnit:    "device",
			ContractType: "subscription",
			Currency:     "JPY",
		},
	})
}

func (h *licenseHandlers) renderForm(w http.ResponseWriter, r *http.Request, status int, props licenseview.FormProps) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	role := middleware.RoleFrom(r.Context())
	if err := licenseview.Form(role, props).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render licenses form", "err", err)
	}
}

// create は POST /licenses の新規作成。fs_dir_path は選択された product の
// vendor 名 / product 名 / license_slug から仕様 §3.2 の slug 規則で計算し
// DB に保存する (物理ディレクトリの作成は L-3 の責務)。
func (h *licenseHandlers) create(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	products, depts, err := h.loadLicenseFormRefs(r, q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load refs for create license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	in, parsed, errs := decodeLicenseForm(r, products, depts, nil)
	formProps := licenseview.FormProps{
		Action:      "/licenses",
		Title:       "ライセンス新規作成",
		Submit:      "作成",
		Products:    products,
		Departments: depts,
		Input:       in,
		Errors:      errs,
	}
	if len(errs) > 0 {
		h.renderForm(w, r, http.StatusBadRequest, formProps)
		return
	}

	// fs_dir_path は他ライセンスとの重複 (DB) と空でない既存物理ディレクトリ
	// の両方を見て _2, _3... サフィックスで衝突回避する (仕様 §3.2)。
	fsDir, err := h.resolveLicenseFsDir(r.Context(), q,
		licenseFsDirPath(parsed.VendorName, parsed.ProductName, parsed.LicenseSlug), 0)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve fs_dir_path for create license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	lic, err := q.CreateLicense(r.Context(), repository.CreateLicenseParams{
		ProductID:          parsed.ProductID,
		OwningDepartmentID: parsed.DepartmentID,
		LicenseSlug:        parsed.LicenseSlug,
		DisplayName:        parsed.DisplayName,
		TotalCount:         parsed.TotalCount,
		CountUnit:          parsed.CountUnit,
		ContractType:       parsed.ContractType,
		PurchasedAt:        parsed.PurchasedAt,
		StartedAt:          parsed.StartedAt,
		ExpiresAt:          parsed.ExpiresAt,
		VendorOrderNo:      parsed.VendorOrderNo,
		Purchaser:          parsed.Purchaser,
		UnitPrice:          parsed.UnitPrice,
		Currency:           &parsed.Currency,
		ProductKeys:        nilIfEmpty(parsed.ProductKeys),
		FsDirPath:          fsDir,
		Note:               parsed.Note,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			formProps.Errors = map[string]string{
				"license_slug": "同じ製品・所管部署・スラッグのライセンスが重複しています",
			}
			h.renderForm(w, r, http.StatusConflict, formProps)
			return
		}
		h.logger.ErrorContext(r.Context(), "create license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 物理ディレクトリ + meta.yml。失敗しても作成自体は成立させる
	// (FS/DB のズレは警告のみでブロックしない思想。appmgr-generate-meta で
	// 回復可能)。
	if err := h.regenerateLicenseFS(r.Context(), q, lic.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "materialize license dir/meta after create",
			"license_id", lic.ID, "err", err)
	}

	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(lic.ID, 10), http.StatusSeeOther)
}

// editForm は GET /licenses/:id/edit の編集フォーム。product_keys は
// write-only のため既存値をプリフィルしない (空欄 = 変更なし)。
// 所管部署が既に廃止されている場合は、その 1 件だけ「(廃止)」付きで
// select に残す。
func (h *licenseHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	row, err := q.GetLicenseByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license for edit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	products, depts, err := h.loadLicenseFormRefs(r, q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load refs for edit license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pinned, err := resolvePinnedDepartment(r, q, depts, &row.OwningDepartmentID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resolve pinned department for edit license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderForm(w, r, http.StatusOK, licenseview.FormProps{
		Action:           "/licenses/" + strconv.FormatInt(row.ID, 10),
		Title:            "ライセンス編集",
		Submit:           "更新",
		IsEdit:           true,
		Products:         products,
		Departments:      depts,
		PinnedDepartment: pinned,
		Input:            formInputFromLicense(row),
	})
}

// update は POST /licenses/:id の更新。product_keys は空欄なら既存値を
// 保持し、入力があれば上書きする。product / 部署 / license_slug の変更に
// 追随して fs_dir_path も再計算する。
func (h *licenseHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	q := repository.New(h.db)
	existing, err := q.GetLicenseByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license for update", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	products, depts, err := h.loadLicenseFormRefs(r, q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load refs for update license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 現在の所管部署が廃止済みでも「そのまま維持」は許す (編集フォームの
	// pinned option に対応)。別の廃止部署への付け替えは弾く。
	in, parsed, errs := decodeLicenseForm(r, products, depts, &existing.OwningDepartmentID)
	if len(errs) > 0 {
		pinned, perr := resolvePinnedDepartment(r, q, depts, &existing.OwningDepartmentID)
		if perr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.renderForm(w, r, http.StatusBadRequest, licenseview.FormProps{
			Action:           "/licenses/" + strconv.FormatInt(id, 10),
			Title:            "ライセンス編集",
			Submit:           "更新",
			IsEdit:           true,
			Products:         products,
			Departments:      depts,
			PinnedDepartment: pinned,
			Input:            in,
			Errors:           errs,
		})
		return
	}

	// write-only 運用: 空欄 = 既存キー保持、入力あり = 上書き。
	keys := existing.ProductKeys
	if parsed.ProductKeys != "" {
		keys = &parsed.ProductKeys
	}

	// fs_dir_path が変わる場合は DB 更新前に物理ディレクトリを追随させる
	// (rename 失敗時に DB と FS がズレない順序。仕様 §3.2 / Plan の決定)。
	fsDir := existing.FsDirPath
	if want := licenseFsDirPath(parsed.VendorName, parsed.ProductName, parsed.LicenseSlug); want != existing.FsDirPath {
		fsDir, err = h.resolveLicenseFsDir(r.Context(), q, want, id)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "resolve fs_dir_path for update license", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.renameLicenseDir(existing.FsDirPath, fsDir); err != nil {
			h.logger.ErrorContext(r.Context(), "rename license dir", "err", err,
				"from", existing.FsDirPath, "to", fsDir)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	affected, err := q.UpdateLicense(r.Context(), repository.UpdateLicenseParams{
		ProductID:          parsed.ProductID,
		OwningDepartmentID: parsed.DepartmentID,
		LicenseSlug:        parsed.LicenseSlug,
		DisplayName:        parsed.DisplayName,
		TotalCount:         parsed.TotalCount,
		CountUnit:          parsed.CountUnit,
		ContractType:       parsed.ContractType,
		PurchasedAt:        parsed.PurchasedAt,
		StartedAt:          parsed.StartedAt,
		ExpiresAt:          parsed.ExpiresAt,
		VendorOrderNo:      parsed.VendorOrderNo,
		Purchaser:          parsed.Purchaser,
		UnitPrice:          parsed.UnitPrice,
		Currency:           &parsed.Currency,
		ProductKeys:        keys,
		FsDirPath:          fsDir,
		Note:               parsed.Note,
		ID:                 id,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			pinned, perr := resolvePinnedDepartment(r, q, depts, &existing.OwningDepartmentID)
			if perr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.renderForm(w, r, http.StatusConflict, licenseview.FormProps{
				Action:           "/licenses/" + strconv.FormatInt(id, 10),
				Title:            "ライセンス編集",
				Submit:           "更新",
				IsEdit:           true,
				Products:         products,
				Departments:      depts,
				PinnedDepartment: pinned,
				Input:            in,
				Errors: map[string]string{
					"license_slug": "同じ製品・所管部署・スラッグのライセンスが重複しています",
				},
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "update license", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		http.NotFound(w, r)
		return
	}

	// 更新後の内容で meta.yml を再生成 (ディレクトリが無ければ MkdirAll で
	// 自然回復)。失敗は error ログのみでブロックしない。
	if err := h.regenerateLicenseFS(r.Context(), q, id); err != nil {
		h.logger.ErrorContext(r.Context(), "regenerate license dir/meta after update",
			"license_id", id, "err", err)
	}

	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// licenseFsDirPath は仕様 §3.2 のレイアウト
// licenses/<vendor_slug>/<product_slug>/<license_slug> を組み立てる
// (base_path からの相対、区切りは常に /)。
func licenseFsDirPath(vendorName, productName, licenseSlug string) string {
	return path.Join("licenses",
		slug.Slugify(vendorName),
		slug.Slugify(productName),
		slug.Slugify(licenseSlug))
}

// licenseParsed は decodeLicenseForm がパースした sqlc 用パラメータ。
// VendorName / ProductName は fs_dir_path の計算用に、選択された product の
// JOIN 済み行から写す。
type licenseParsed struct {
	ProductID     int64
	DepartmentID  int64
	VendorName    string
	ProductName   string
	LicenseSlug   string
	DisplayName   string
	TotalCount    *int64
	CountUnit     string
	ContractType  string
	PurchasedAt   *time.Time
	StartedAt     *time.Time
	ExpiresAt     *time.Time
	VendorOrderNo *string
	Purchaser     *string
	UnitPrice     *int64
	Currency      string
	ProductKeys   string
	Note          *string
}

// decodeLicenseForm は POST フォームから入力を取り出して検証する。
// products / depts は select の選択肢と同じ集合を渡し、選択値の実在確認を
// 兼ねる (廃止部署は depts に含まれないため自然に弾かれる)。
// allowDeptID が非 nil の場合、その部署 ID だけは depts 外でも許可する
// (編集中ライセンスの所管部署が廃止済みのケース)。
func decodeLicenseForm(r *http.Request, products []repository.ListProductsRow, depts []repository.Department, allowDeptID *int64) (licenseview.FormInput, licenseParsed, map[string]string) {
	_ = r.ParseForm()
	in := licenseview.FormInput{
		ProductID:     strings.TrimSpace(r.PostFormValue("product_id")),
		DepartmentID:  strings.TrimSpace(r.PostFormValue("owning_department_id")),
		LicenseSlug:   strings.TrimSpace(r.PostFormValue("license_slug")),
		DisplayName:   strings.TrimSpace(r.PostFormValue("display_name")),
		TotalCount:    strings.TrimSpace(r.PostFormValue("total_count")),
		CountUnit:     strings.TrimSpace(r.PostFormValue("count_unit")),
		ContractType:  strings.TrimSpace(r.PostFormValue("contract_type")),
		PurchasedAt:   strings.TrimSpace(r.PostFormValue("purchased_at")),
		StartedAt:     strings.TrimSpace(r.PostFormValue("started_at")),
		ExpiresAt:     strings.TrimSpace(r.PostFormValue("expires_at")),
		VendorOrderNo: strings.TrimSpace(r.PostFormValue("vendor_order_no")),
		Purchaser:     strings.TrimSpace(r.PostFormValue("purchaser")),
		UnitPrice:     strings.TrimSpace(r.PostFormValue("unit_price")),
		Currency:      strings.TrimSpace(r.PostFormValue("currency")),
		Note:          r.PostFormValue("note"),
	}
	errs := map[string]string{}
	// product_keys は view の FormInput に含めない (write-only。エラー
	// 再描画でも平文を HTML に戻さないため、server 側の parsed のみが持つ)
	parsed := licenseParsed{
		LicenseSlug:  in.LicenseSlug,
		DisplayName:  in.DisplayName,
		CountUnit:    in.CountUnit,
		ContractType: in.ContractType,
		Currency:     in.Currency,
		ProductKeys:  strings.TrimSpace(r.PostFormValue("product_keys")),
		Note:         nilIfEmpty(in.Note),
	}

	if in.ProductID == "" {
		errs["product_id"] = "製品を選択してください"
	} else if pid, err := strconv.ParseInt(in.ProductID, 10, 64); err != nil {
		errs["product_id"] = "不正な製品 ID です"
	} else {
		found := false
		for _, p := range products {
			if p.ID == pid {
				parsed.ProductID = pid
				parsed.VendorName = p.VendorName
				parsed.ProductName = p.CanonicalName
				found = true
				break
			}
		}
		if !found {
			errs["product_id"] = "製品が存在しません"
		}
	}

	if in.DepartmentID == "" {
		errs["owning_department_id"] = "所管部署を選択してください"
	} else if did, err := strconv.ParseInt(in.DepartmentID, 10, 64); err != nil {
		errs["owning_department_id"] = "不正な部署 ID です"
	} else {
		found := allowDeptID != nil && *allowDeptID == did
		if !found {
			for _, d := range depts {
				if d.ID == did {
					found = true
					break
				}
			}
		}
		if found {
			parsed.DepartmentID = did
		} else {
			errs["owning_department_id"] = "現役の部署を選択してください"
		}
	}

	if in.LicenseSlug == "" {
		errs["license_slug"] = "スラッグは必須です"
	}
	if in.DisplayName == "" {
		errs["display_name"] = "表示名は必須です"
	}

	switch in.CountUnit {
	case "device", "user":
	default:
		errs["count_unit"] = "本数単位を選択してください"
	}
	switch in.ContractType {
	case "perpetual", "subscription":
	default:
		errs["contract_type"] = "契約形態を選択してください"
	}

	if v, msg := parseNonNegativeIntOpt(in.TotalCount); msg != "" {
		errs["total_count"] = msg
	} else {
		parsed.TotalCount = v
	}
	if v, msg := parseNonNegativeIntOpt(in.UnitPrice); msg != "" {
		errs["unit_price"] = msg
	} else {
		parsed.UnitPrice = v
	}

	if v, msg := parseDateOpt(in.PurchasedAt); msg != "" {
		errs["purchased_at"] = msg
	} else {
		parsed.PurchasedAt = v
	}
	if v, msg := parseDateOpt(in.StartedAt); msg != "" {
		errs["started_at"] = msg
	} else {
		parsed.StartedAt = v
	}
	if v, msg := parseDateOpt(in.ExpiresAt); msg != "" {
		errs["expires_at"] = msg
	} else {
		parsed.ExpiresAt = v
	}

	if parsed.Currency == "" {
		parsed.Currency = "JPY"
	}
	parsed.VendorOrderNo = nilIfEmpty(in.VendorOrderNo)
	parsed.Purchaser = nilIfEmpty(in.Purchaser)

	return in, parsed, errs
}

// formInputFromLicense は既存レコードを編集フォーム入力値に詰め直す。
// ProductKeys は write-only のため意図的に空にする (空欄提出 = 変更なし)。
func formInputFromLicense(l repository.GetLicenseByIDRow) licenseview.FormInput {
	in := licenseview.FormInput{
		ProductID:     strconv.FormatInt(l.ProductID, 10),
		DepartmentID:  strconv.FormatInt(l.OwningDepartmentID, 10),
		LicenseSlug:   l.LicenseSlug,
		DisplayName:   l.DisplayName,
		CountUnit:     l.CountUnit,
		ContractType:  l.ContractType,
		VendorOrderNo: derefString(l.VendorOrderNo),
		Purchaser:     derefString(l.Purchaser),
		Currency:      derefString(l.Currency),
		Note:          derefString(l.Note),
	}
	if l.TotalCount != nil {
		in.TotalCount = strconv.FormatInt(*l.TotalCount, 10)
	}
	if l.UnitPrice != nil {
		in.UnitPrice = strconv.FormatInt(*l.UnitPrice, 10)
	}
	if l.PurchasedAt != nil {
		in.PurchasedAt = l.PurchasedAt.Format("2006-01-02")
	}
	if l.StartedAt != nil {
		in.StartedAt = l.StartedAt.Format("2006-01-02")
	}
	if l.ExpiresAt != nil {
		in.ExpiresAt = l.ExpiresAt.Format("2006-01-02")
	}
	return in
}

// parseNonNegativeIntOpt は string を *int64 にする。空文字は nil
// (total_count なら「無制限」、unit_price なら「未入力」)。形式不正や
// 負数は (nil, エラーメッセージ) を返す。
func parseNonNegativeIntOpt(s string) (*int64, string) {
	if s == "" {
		return nil, ""
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return nil, "0 以上の整数で入力してください"
	}
	return &v, ""
}
