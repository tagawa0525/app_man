package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// DevicesImporter は devices テーブルへの CSV 一括投入。
// CSV ヘッダ: asset_code,hostname,primary_user_code,department_code
//
//   - asset_code 必須・UNIQUE
//   - hostname 任意 (空欄は NULL)
//   - primary_user_code 任意 (users.employee_code を引いて FK 解決)
//   - department_code 任意
type DevicesImporter struct{}

func (DevicesImporter) Kind() string { return "devices" }
func (DevicesImporter) HeaderColumns() []string {
	return []string{"asset_code", "hostname", "primary_user_code", "department_code"}
}

func (DevicesImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// DB 既存 asset_code
	existing := map[string]struct{}{}
	ds, err := q.ListDevicesIncludingInactive(ctx)
	if err != nil {
		return []ValidationError{{Line: 0, Column: "", Message: "list devices: " + err.Error()}}
	}
	for _, d := range ds {
		existing[d.AssetCode] = struct{}{}
	}

	seen := map[string]int{}

	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["asset_code"])
		user := strings.TrimSpace(r.Fields["primary_user_code"])
		dept := strings.TrimSpace(r.Fields["department_code"])

		if code == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "資産コードは必須です"})
		}
		if code != "" {
			if _, ok := existing[code]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "DB に既に登録されています: " + code})
			}
			if prev, ok := seen[code]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
			} else {
				seen[code] = r.Line
			}
		}

		// FK
		if user != "" {
			if _, err := q.GetUserByEmployeeCode(ctx, user); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					errs = append(errs, ValidationError{Line: r.Line, Column: "primary_user_code", Message: "ユーザ '" + user + "' が見つかりません"})
				} else {
					errs = append(errs, ValidationError{Line: r.Line, Column: "primary_user_code", Message: "lookup error: " + err.Error()})
				}
			}
		}
		if dept != "" {
			if _, err := q.GetDepartmentByCode(ctx, dept); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "部署 '" + dept + "' が見つかりません"})
				} else {
					errs = append(errs, ValidationError{Line: r.Line, Column: "department_code", Message: "lookup error: " + err.Error()})
				}
			}
		}
	}
	return errs
}

func (DevicesImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["asset_code"])
		hostname := strings.TrimSpace(r.Fields["hostname"])
		user := strings.TrimSpace(r.Fields["primary_user_code"])
		dept := strings.TrimSpace(r.Fields["department_code"])

		params := repository.CreateDeviceParams{
			AssetCode: code,
			Hostname:  nilIfEmpty(hostname),
		}
		if user != "" {
			u, err := q.GetUserByEmployeeCode(ctx, user)
			if err != nil {
				return 0, errors.New("line " + itoa(r.Line) + ": resolve user: " + err.Error())
			}
			params.PrimaryUserID = &u.ID
		}
		if dept != "" {
			d, err := q.GetDepartmentByCode(ctx, dept)
			if err != nil {
				return 0, errors.New("line " + itoa(r.Line) + ": resolve department: " + err.Error())
			}
			params.DepartmentID = &d.ID
		}
		if _, err := q.CreateDevice(ctx, params); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}
