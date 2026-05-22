package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// VendorsImporter は vendors テーブルへの CSV 一括投入。
// CSV ヘッダ: name,url,note
//
//   - name 必須・UNIQUE
//   - url 任意・http(s) スキーム必須 + ホスト必須
//   - note 任意
type VendorsImporter struct{}

func (VendorsImporter) Kind() string            { return "vendors" }
func (VendorsImporter) HeaderColumns() []string { return []string{"name", "url", "note"} }

func (VendorsImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// DB 既存の vendor name セット
	existing := map[string]struct{}{}
	vs, err := q.ListVendors(ctx)
	if err != nil {
		return []ValidationError{{Line: 0, Column: "", Message: "list vendors: " + err.Error()}}
	}
	for _, v := range vs {
		existing[v.Name] = struct{}{}
	}

	// CSV 内重複検出
	seen := map[string]int{}

	for _, r := range rows {
		name := strings.TrimSpace(r.Fields["name"])
		if name == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "name", Message: "名前は必須です"})
		}
		if _, ok := existing[name]; name != "" && ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "name", Message: "DB に既に登録されています: " + name})
		}
		if prev, ok := seen[name]; name != "" && ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "name", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
		} else if name != "" {
			seen[name] = r.Line
		}

		urlStr := strings.TrimSpace(r.Fields["url"])
		if msg := validateHTTPURLForCSV(urlStr); msg != "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "url", Message: msg})
		}
	}
	return errs
}

func (VendorsImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		params := repository.CreateVendorParams{
			Name: strings.TrimSpace(r.Fields["name"]),
			Url:  nilIfEmpty(strings.TrimSpace(r.Fields["url"])),
			Note: nilIfEmpty(r.Fields["note"]),
		}
		if _, err := q.CreateVendor(ctx, params); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}

// validateHTTPURLForCSV は handler/web/vendors.go の validateHTTPURL と
// 同型 (URL は空欄許容、ある場合は http/https + ホスト必須)。
// 3 度登場時点で共通化を検討する (本 PR では複製)。
func validateHTTPURLForCSV(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
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
