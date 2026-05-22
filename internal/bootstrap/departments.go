package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/repository"
)

// DepartmentsImporter は departments テーブルへの CSV 一括投入。
// CSV ヘッダ: code,name,parent_code,valid_from,valid_to,source_ou
//
//   - code 必須・UNIQUE
//   - name 必須
//   - parent_code 任意。CSV 内では **先行する行で登録済みの code** または
//     DB 既存を指す (後続行の code は参照不可)
//   - valid_from / valid_to は YYYY-MM-DD 形式、空欄可
//   - source は 'manual' 固定 (DDL デフォルト)
//   - successor_department_id は本 PR では未対応 (空欄、後続 PR で扱う)
type DepartmentsImporter struct{}

func (DepartmentsImporter) Kind() string { return "departments" }
func (DepartmentsImporter) HeaderColumns() []string {
	return []string{"code", "name", "parent_code", "valid_from", "valid_to", "source_ou"}
}

func (DepartmentsImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// CSV 内 code (同行より前で登録された code の先行参照を許す)
	seenInCSV := map[string]int{}

	// DB 既存 code lookup の結果をキャッシュ (parent_code が同じ DB 既存を
	// 何度も指す可能性に備えて。ListDepartments 全件取得は LIMIT 200 で
	// 取りこぼすため使えず、1 件ずつ GetDepartmentByCode で確認する)。
	dbCache := map[string]bool{} // value: 存在するか

	existsInDB := func(code string) (bool, error) {
		if v, ok := dbCache[code]; ok {
			return v, nil
		}
		_, err := q.GetDepartmentByCode(ctx, code)
		switch {
		case err == nil:
			dbCache[code] = true
			return true, nil
		case errors.Is(err, sql.ErrNoRows):
			dbCache[code] = false
			return false, nil
		default:
			return false, err
		}
	}

	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["code"])
		name := strings.TrimSpace(r.Fields["name"])
		parent := strings.TrimSpace(r.Fields["parent_code"])
		vf := strings.TrimSpace(r.Fields["valid_from"])
		vt := strings.TrimSpace(r.Fields["valid_to"])

		if code == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "code", Message: "コードは必須です"})
		}
		if name == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "name", Message: "名前は必須です"})
		}

		// DB / CSV 内重複
		if code != "" {
			inDB, err := existsInDB(code)
			if err != nil {
				errs = append(errs, ValidationError{Line: r.Line, Column: "code", Message: "lookup error: " + err.Error()})
			} else if inDB {
				errs = append(errs, ValidationError{Line: r.Line, Column: "code", Message: "DB に既に登録されています: " + code})
			}
			if prev, ok := seenInCSV[code]; ok {
				errs = append(errs, ValidationError{Line: r.Line, Column: "code", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
			} else {
				seenInCSV[code] = r.Line
			}
		}

		// parent_code は CSV 内で先行する行 or DB 既存を参照可能
		if parent != "" {
			_, inCSV := seenInCSV[parent]
			inDB, lookupErr := existsInDB(parent)
			if lookupErr != nil {
				errs = append(errs, ValidationError{Line: r.Line, Column: "parent_code", Message: "lookup error: " + lookupErr.Error()})
			} else if !inCSV && !inDB {
				errs = append(errs, ValidationError{Line: r.Line, Column: "parent_code", Message: "親部署 '" + parent + "' が未登録です (CSV 内では同行より前に書くか、DB に既存である必要があります)"})
			}
			if parent == code {
				errs = append(errs, ValidationError{Line: r.Line, Column: "parent_code", Message: "自分自身を親にできません"})
			}
		}

		// 日付形式
		if vf != "" {
			if _, err := time.Parse("2006-01-02", vf); err != nil {
				errs = append(errs, ValidationError{Line: r.Line, Column: "valid_from", Message: "YYYY-MM-DD 形式で入力してください"})
			}
		}
		if vt != "" {
			if _, err := time.Parse("2006-01-02", vt); err != nil {
				errs = append(errs, ValidationError{Line: r.Line, Column: "valid_to", Message: "YYYY-MM-DD 形式で入力してください"})
			}
		}
	}
	return errs
}

func (DepartmentsImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		code := strings.TrimSpace(r.Fields["code"])
		name := strings.TrimSpace(r.Fields["name"])
		parent := strings.TrimSpace(r.Fields["parent_code"])
		vf := strings.TrimSpace(r.Fields["valid_from"])

		params := repository.CreateDepartmentParams{
			Code: code,
			Name: name,
		}
		if parent != "" {
			p, err := q.GetDepartmentByCode(ctx, parent)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return 0, errors.New("line " + itoa(r.Line) + ": parent_code '" + parent + "' not found")
				}
				return 0, errors.New("line " + itoa(r.Line) + ": resolve parent: " + err.Error())
			}
			params.ParentID = &p.ID
		}
		if vf != "" {
			t, err := time.Parse("2006-01-02", vf)
			if err != nil {
				return 0, errors.New("line " + itoa(r.Line) + ": parse valid_from: " + err.Error())
			}
			params.ValidFrom = &t
		}
		if _, err := q.CreateDepartment(ctx, params); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
		// 注: valid_to / source_ou は CreateDepartment クエリのパラメータに
		//     含まれていないため、本 PR では未対応 (department CRUD の
		//     更新フォームでも valid_to は別の SoftDelete 経路で立てる)。
		//     必要なら別 PR でクエリ追加。
	}
	return len(rows), nil
}
