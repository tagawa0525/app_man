package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/slug"
)

// LicensesImporter は licenses テーブルへの CSV 一括投入。
// CSV ヘッダ: vendor_name,product_name,edition,department_code,license_slug,
// display_name,total_count,count_unit,contract_type,purchased_at,started_at,
// expires_at,vendor_order_no,purchaser,unit_price,currency,product_keys,note
//
//   - vendor_name / product_name / department_code / license_slug /
//     display_name 必須。vendor は name、product は (vendor, canonical_name,
//     edition)、department は code で自然キー解決 (廃止部署は不可 — web の
//     新規作成と同基準)
//   - edition 任意 ("" は NULL。products の UNIQUE と同じ規則)
//   - count_unit は device / user、contract_type は perpetual / subscription
//   - 日付 3 列は YYYY-MM-DD (空欄は NULL)、total_count / unit_price は
//     0 以上の整数 (空欄は NULL)、currency 空欄は JPY
//   - fs_dir_path は web と同じ規則 (licenses/<v>/<p>/<slug>) で計算して
//     DB に保存するのみ。CSV 内・DB 既存との衝突は検証エラー (web の
//     _2 サフィックス回避はせず、Excel 側で解消させる)。物理ディレクトリと
//     meta.yml は作らない — 投入後に appmgr-generate-meta を 1 回実行する
//
// UNIQUE 制約: (product_id, owning_department_id, license_slug)
type LicensesImporter struct{}

func (LicensesImporter) Kind() string { return "licenses" }
func (LicensesImporter) HeaderColumns() []string {
	return []string{
		"vendor_name", "product_name", "edition", "department_code",
		"license_slug", "display_name", "total_count", "count_unit",
		"contract_type", "purchased_at", "started_at", "expires_at",
		"vendor_order_no", "purchaser", "unit_price", "currency",
		"product_keys", "note",
	}
}

func (LicensesImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// CSV 内重複検出: 自然キー (vendor, product, edition, dept, slug) と
	// fs_dir_path の 2 系統。fs_dir_path は部署違いの行が同じパスに落ちる
	// ことがあるため自然キーとは別に見る。
	type natKey struct{ vendor, product, edition, dept, slug string }
	seenNat := map[natKey]int{}
	seenFsDir := map[string]int{}

	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["vendor_name"])
		product := strings.TrimSpace(r.Fields["product_name"])
		edition := strings.TrimSpace(r.Fields["edition"])
		dept := strings.TrimSpace(r.Fields["department_code"])
		licSlug := strings.TrimSpace(r.Fields["license_slug"])

		if vendor == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "ベンダー名は必須です"})
		}
		if product == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "product_name", Message: "製品名は必須です"})
		}
		if dept == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "部署コードは必須です"})
		}
		if licSlug == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "スラッグは必須です"})
		}
		if strings.TrimSpace(r.Fields["display_name"]) == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "display_name", Message: "表示名は必須です"})
		}

		// 参照解決 (vendor → product)。product の存在確認には vendor が必要。
		var productID *int64
		if vendor != "" {
			v, err := q.GetVendorByName(ctx, vendor)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "ベンダー '" + vendor + "' が見つかりません"})
			case err != nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "lookup error: " + err.Error()})
			case product != "":
				p, perr := getProductByKeyForBootstrap(ctx, q, v.ID, product, edition)
				switch {
				case errors.Is(perr, sql.ErrNoRows):
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_name", Message: "製品 '" + product + "' が見つかりません"})
				case perr != nil:
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_name", Message: "lookup error: " + perr.Error()})
				default:
					productID = &p.ID
				}
			}
		}

		// department は現役のみ (web の新規作成フォームと同基準)。
		var deptID *int64
		if dept != "" {
			d, err := q.GetDepartmentByCode(ctx, dept)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "部署 '" + dept + "' が見つかりません"})
			case err != nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "lookup error: " + err.Error()})
			case d.ValidTo != nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "廃止済みの部署です"})
			default:
				deptID = &d.ID
			}
		}

		// 自然キーの DB 既存重複
		if productID != nil && deptID != nil && licSlug != "" {
			_, err := q.GetLicenseByKey(ctx, repository.GetLicenseByKeyParams{
				ProductID: *productID, OwningDepartmentID: *deptID, LicenseSlug: licSlug,
			})
			switch {
			case err == nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "DB に既に登録されています"})
			case !errors.Is(err, sql.ErrNoRows):
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "lookup error: " + err.Error()})
			}
		}

		// 自然キーの CSV 内重複
		if vendor != "" && product != "" && dept != "" && licSlug != "" {
			k := natKey{vendor, product, edition, dept, licSlug}
			if prev, ok := seenNat[k]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
			} else {
				seenNat[k] = r.Line
			}
		}

		// fs_dir_path の衝突 (DB 既存・CSV 内)。物理 FS には触れないため
		// ここでエラーにして Excel 側で解消させる。
		if vendor != "" && product != "" && licSlug != "" {
			fsDir := licenseFsDir(vendor, product, licSlug)
			cnt, err := q.CountLicensesByFsDirPath(ctx, repository.CountLicensesByFsDirPathParams{
				FsDirPath: fsDir, ID: 0,
			})
			switch {
			case err != nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "lookup error: " + err.Error()})
			case cnt > 0:
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "fs_dir_path が DB の既存ライセンスと衝突します: " + fsDir})
			}
			if prev, ok := seenFsDir[fsDir]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "license_slug", Message: "fs_dir_path が CSV 内で衝突します (line " + itoa(prev) + ")"})
			} else {
				seenFsDir[fsDir] = r.Line
			}
		}

		// enum
		cu := strings.TrimSpace(r.Fields["count_unit"])
		if cu != "device" && cu != "user" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "count_unit", Message: "device / user のいずれかにしてください"})
		}
		ct := strings.TrimSpace(r.Fields["contract_type"])
		if ct != "perpetual" && ct != "subscription" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "contract_type", Message: "perpetual / subscription のいずれかにしてください"})
		}

		// 数値 (空欄 = NULL)
		for _, col := range []string{"total_count", "unit_price"} {
			if _, ok := parseNonNegativeOpt(strings.TrimSpace(r.Fields[col])); !ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: col, Message: "0 以上の整数で入力してください"})
			}
		}

		// 日付 (空欄 = NULL)
		for _, col := range []string{"purchased_at", "started_at", "expires_at"} {
			if _, ok := parseDateOpt(strings.TrimSpace(r.Fields[col])); !ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: col, Message: "YYYY-MM-DD 形式で入力してください"})
			}
		}
	}
	return errs
}

func (LicensesImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["vendor_name"])
		product := strings.TrimSpace(r.Fields["product_name"])
		edition := strings.TrimSpace(r.Fields["edition"])
		dept := strings.TrimSpace(r.Fields["department_code"])
		licSlug := strings.TrimSpace(r.Fields["license_slug"])

		v, err := q.GetVendorByName(ctx, vendor)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve vendor: " + err.Error())
		}
		p, err := getProductByKeyForBootstrap(ctx, q, v.ID, product, edition)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve product: " + err.Error())
		}
		d, err := q.GetDepartmentByCode(ctx, dept)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve department: " + err.Error())
		}

		totalCount, _ := parseNonNegativeOpt(strings.TrimSpace(r.Fields["total_count"]))
		unitPrice, _ := parseNonNegativeOpt(strings.TrimSpace(r.Fields["unit_price"]))
		purchasedAt, _ := parseDateOpt(strings.TrimSpace(r.Fields["purchased_at"]))
		startedAt, _ := parseDateOpt(strings.TrimSpace(r.Fields["started_at"]))
		expiresAt, _ := parseDateOpt(strings.TrimSpace(r.Fields["expires_at"]))
		currency := defaultIfEmpty(strings.TrimSpace(r.Fields["currency"]), "JPY")

		if _, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
			ProductID:          p.ID,
			OwningDepartmentID: d.ID,
			LicenseSlug:        licSlug,
			DisplayName:        strings.TrimSpace(r.Fields["display_name"]),
			TotalCount:         totalCount,
			CountUnit:          strings.TrimSpace(r.Fields["count_unit"]),
			ContractType:       strings.TrimSpace(r.Fields["contract_type"]),
			PurchasedAt:        purchasedAt,
			StartedAt:          startedAt,
			ExpiresAt:          expiresAt,
			VendorOrderNo:      nilIfEmpty(strings.TrimSpace(r.Fields["vendor_order_no"])),
			Purchaser:          nilIfEmpty(strings.TrimSpace(r.Fields["purchaser"])),
			UnitPrice:          unitPrice,
			Currency:           &currency,
			ProductKeys:        nilIfEmpty(strings.TrimSpace(r.Fields["product_keys"])),
			FsDirPath:          licenseFsDir(vendor, product, licSlug),
			Note:               nilIfEmpty(r.Fields["note"]),
		}); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}

// licenseFsDir は仕様 §3.2 のレイアウト
// licenses/<vendor_slug>/<product_slug>/<license_slug> を組み立てる
// (web の licenseFsDirPath と同じ規則。base_path 相対、区切りは常に /)。
func licenseFsDir(vendorName, productName, licenseSlug string) string {
	return path.Join("licenses",
		slug.Slugify(vendorName),
		slug.Slugify(productName),
		slug.Slugify(licenseSlug))
}

// parseNonNegativeOpt は "" / 0 以上の整数文字列を *int64 に変換する。
// 空欄は (nil, true)。形式不正・負数は (nil, false)。
func parseNonNegativeOpt(s string) (*int64, bool) {
	if s == "" {
		return nil, true
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return nil, false
	}
	return &v, true
}

// parseDateOpt は "" / YYYY-MM-DD を *time.Time に変換する。
// 空欄は (nil, true)。形式不正は (nil, false)。
func parseDateOpt(s string) (*time.Time, bool) {
	if s == "" {
		return nil, true
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, false
	}
	return &t, true
}
