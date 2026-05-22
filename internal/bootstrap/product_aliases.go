package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// ProductAliasesImporter は product_aliases テーブルへの CSV 一括投入。
// CSV ヘッダ: product_vendor_name,product_canonical_name,product_edition,alias_name
//
//   - product_* 3 列で products を FK 解決 (edition は空欄可)
//   - alias_name 必須・UNIQUE
type ProductAliasesImporter struct{}

func (ProductAliasesImporter) Kind() string { return "product_aliases" }
func (ProductAliasesImporter) HeaderColumns() []string {
	return []string{"product_vendor_name", "product_canonical_name", "product_edition", "alias_name"}
}

func (ProductAliasesImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	seen := map[string]int{}

	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["product_vendor_name"])
		name := strings.TrimSpace(r.Fields["product_canonical_name"])
		edition := strings.TrimSpace(r.Fields["product_edition"])
		alias := strings.TrimSpace(r.Fields["alias_name"])

		if vendor == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "product_vendor_name", Message: "ベンダー名は必須です"})
		}
		if name == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "product_canonical_name", Message: "製品名は必須です"})
		}
		if alias == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "alias_name", Message: "別名は必須です"})
			continue
		}

		// products FK 解決
		if vendor != "" && name != "" {
			v, err := q.GetVendorByName(ctx, vendor)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_vendor_name", Message: "ベンダー '" + vendor + "' が見つかりません"})
				} else {
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_vendor_name", Message: "lookup error: " + err.Error()})
				}
			} else if _, err := getProductByKeyForBootstrap(ctx, q, v.ID, name, edition); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_canonical_name", Message: "製品 '" + name + "' (edition='" + edition + "') が見つかりません"})
				} else {
					errs = append(errs, ValidationError{Line: r.Line, Column: "product_canonical_name", Message: "lookup error: " + err.Error()})
				}
			}
		}

		// alias_name の DB 重複
		_, aerr := q.GetAliasByName(ctx, alias)
		switch {
		case aerr == nil:
			errs = append(errs, ValidationError{Line: r.Line, Column: "alias_name", Message: "別名 '" + alias + "' は DB に既に登録されています"})
		case errors.Is(aerr, sql.ErrNoRows):
			// 未登録 — OK
		default:
			errs = append(errs, ValidationError{Line: r.Line, Column: "alias_name", Message: "lookup error: " + aerr.Error()})
		}

		// CSV 内重複
		if prev, ok := seen[alias]; ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "alias_name", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
		} else {
			seen[alias] = r.Line
		}
	}
	return errs
}

func (ProductAliasesImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		vendor := strings.TrimSpace(r.Fields["product_vendor_name"])
		name := strings.TrimSpace(r.Fields["product_canonical_name"])
		edition := strings.TrimSpace(r.Fields["product_edition"])
		alias := strings.TrimSpace(r.Fields["alias_name"])

		v, err := q.GetVendorByName(ctx, vendor)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve vendor: " + err.Error())
		}
		p, err := getProductByKeyForBootstrap(ctx, q, v.ID, name, edition)
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve product: " + err.Error())
		}
		if _, err := q.CreateAlias(ctx, repository.CreateAliasParams{
			ProductID: p.ID,
			AliasName: alias,
		}); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}
