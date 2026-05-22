package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// ProductsImporter は products テーブルへの CSV 一括投入。
// CSV ヘッダ: vendor_name,canonical_name,edition,software_type,license_required,default_approval_status,note
//
//   - vendor_name 必須 (vendors.name で FK 解決)
//   - canonical_name 必須
//   - edition 任意 ("" は NULL)
//   - software_type 任意 (空欄なら DDL デフォルト "installed")
//   - license_required 任意 ("true"/"false"/"" を *bool に変換)
//   - default_approval_status 任意 (空欄なら DDL デフォルト "unknown")
//   - note 任意
//
// UNIQUE 制約: (vendor_id, canonical_name, edition)
type ProductsImporter struct{}

func (ProductsImporter) Kind() string { return "products" }
func (ProductsImporter) HeaderColumns() []string {
	return []string{
		"vendor_name", "canonical_name", "edition",
		"software_type", "license_required", "default_approval_status", "note",
	}
}

func (ProductsImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// CSV 内重複検出 (vendor_name + canonical_name + edition)
	type key struct{ vendor, name, edition string }
	seen := map[key]int{}

	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["vendor_name"])
		name := strings.TrimSpace(r.Fields["canonical_name"])
		edition := strings.TrimSpace(r.Fields["edition"])

		if vendor == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "ベンダー名は必須です"})
		} else if _, err := q.GetVendorByName(ctx, vendor); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "ベンダー '" + vendor + "' が見つかりません"})
			} else {
				errs = append(errs, ValidationError{Line: r.Line, Column: "vendor_name", Message: "lookup error: " + err.Error()})
			}
		}
		if name == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "canonical_name", Message: "製品名は必須です"})
		}

		// DB 既存重複
		if vendor != "" && name != "" {
			v, err := q.GetVendorByName(ctx, vendor)
			if err == nil {
				if _, derr := getProductByKeyForBootstrap(ctx, q, v.ID, name, edition); derr == nil {
					errs = append(errs, ValidationError{Line: r.Line, Column: "canonical_name", Message: "DB に既に登録されています"})
				}
			}
		}

		// CSV 内重複
		k := key{vendor, name, edition}
		if prev, ok := seen[k]; ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "canonical_name", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
		} else {
			seen[k] = r.Line
		}

		// 任意フィールドの形式チェック
		lr := strings.TrimSpace(r.Fields["license_required"])
		if lr != "" && lr != "true" && lr != "false" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "license_required", Message: "true / false / 空欄のいずれかにしてください"})
		}
		st := strings.TrimSpace(r.Fields["software_type"])
		if st != "" && st != "installed" && st != "saas" && st != "appliance" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "software_type", Message: "installed / saas / appliance のいずれかにしてください"})
		}
		das := strings.TrimSpace(r.Fields["default_approval_status"])
		if das != "" && das != "approved" && das != "denied" && das != "unknown" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "default_approval_status", Message: "approved / denied / unknown のいずれかにしてください"})
		}
	}
	return errs
}

func (ProductsImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["vendor_name"])
		v, err := q.GetVendorByName(ctx, vendor)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve vendor: " + err.Error())
		}
		params := repository.CreateProductParams{
			VendorID:              v.ID,
			CanonicalName:         strings.TrimSpace(r.Fields["canonical_name"]),
			Edition:               nilIfEmpty(strings.TrimSpace(r.Fields["edition"])),
			SoftwareType:          defaultIfEmpty(strings.TrimSpace(r.Fields["software_type"]), "installed"),
			LicenseRequired:       parseBoolOpt(strings.TrimSpace(r.Fields["license_required"])),
			DefaultApprovalStatus: defaultIfEmpty(strings.TrimSpace(r.Fields["default_approval_status"]), "unknown"),
			Note:                  nilIfEmpty(r.Fields["note"]),
		}
		if _, err := q.CreateProduct(ctx, params); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}

// getProductByKeyForBootstrap は edition の NULL / 非 NULL を吸収して
// products 1 件を引く薄いラッパ。
func getProductByKeyForBootstrap(ctx context.Context, q *repository.Queries, vendorID int64, name, edition string) (repository.Product, error) {
	if edition == "" {
		return q.GetProductByKeyWithoutEdition(ctx, repository.GetProductByKeyWithoutEditionParams{
			VendorID:      vendorID,
			CanonicalName: name,
		})
	}
	ed := edition
	return q.GetProductByKeyWithEdition(ctx, repository.GetProductByKeyWithEditionParams{
		VendorID:      vendorID,
		CanonicalName: name,
		Edition:       &ed,
	})
}

// defaultIfEmpty は空文字なら fallback を返す。
func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// parseBoolOpt は "true" / "false" / "" を *bool に変換する。
// 検証側で形式チェック済の前提。
func parseBoolOpt(s string) *bool {
	switch s {
	case "true":
		t := true
		return &t
	case "false":
		f := false
		return &f
	default:
		return nil
	}
}
