package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// UsersImporter は users テーブルへの CSV 一括投入。
// CSV ヘッダ: employee_code,username,name,email,department_code
//
//   - employee_code 必須・UNIQUE
//   - name 必須
//   - username / email 任意 (空欄は NULL)
//   - department_code 任意。空欄か DB に既存の departments.code を指す
type UsersImporter struct{}

func (UsersImporter) Kind() string { return "users" }
func (UsersImporter) HeaderColumns() []string {
	return []string{"employee_code", "username", "name", "email", "department_code"}
}

func (UsersImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	seen := map[string]int{}

	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["employee_code"])
		name := strings.TrimSpace(r.Fields["name"])
		dept := strings.TrimSpace(r.Fields["department_code"])

		if code == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "従業員コードは必須です"})
		}
		if name == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "name", Message: "氏名は必須です"})
		}

		// DB 既存
		if code != "" {
			_, err := q.GetUserByEmployeeCode(ctx, code)
			switch {
			case err == nil:
				errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "DB に既に登録されています: " + code})
			case errors.Is(err, sql.ErrNoRows):
				// 未登録 — OK
			default:
				errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "lookup error: " + err.Error()})
			}
			if prev, ok := seen[code]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
			} else {
				seen[code] = r.Line
			}
		}

		// department_code FK
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

func (UsersImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["employee_code"])
		name := strings.TrimSpace(r.Fields["name"])
		username := strings.TrimSpace(r.Fields["username"])
		email := strings.TrimSpace(r.Fields["email"])
		dept := strings.TrimSpace(r.Fields["department_code"])

		params := repository.CreateUserParams{
			EmployeeCode: code,
			Name:         name,
			Username:     nilIfEmpty(username),
			Email:        nilIfEmpty(email),
		}
		if dept != "" {
			d, err := q.GetDepartmentByCode(ctx, dept)
			if err != nil {
				return 0, errors.New("line " + itoa(r.Line) + ": resolve department: " + err.Error())
			}
			params.DepartmentID = &d.ID
		}
		if _, err := q.CreateUser(ctx, params); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}
