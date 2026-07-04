package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// UserAssignmentsImporter は user_assignments テーブルへの CSV 一括投入。
// CSV ヘッダ: vendor_name,product_name,edition,department_code,license_slug,
// employee_code,external_account_id,note
//
//   - 先頭 5 列はライセンス参照 (licenseRefResolver で自然キー解決)
//   - employee_code 必須。退職者 (deactivated_at NOT NULL) への割当は
//     検証エラー (web の 400 と同基準)
//   - DB のアクティブ割当・CSV 内の重複は検証エラー (web の 409 と同基準。
//     000006 の部分 UNIQUE uniq_user_assignments_active が最終防衛)
//   - external_account_id / note 任意 (空欄は NULL)
type UserAssignmentsImporter struct{}

func (UserAssignmentsImporter) Kind() string { return "user_assignments" }
func (UserAssignmentsImporter) HeaderColumns() []string {
	return []string{
		"vendor_name", "product_name", "edition", "department_code",
		"license_slug", "employee_code", "external_account_id", "note",
	}
}

func (UserAssignmentsImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// CSV 内重複検出 (解決済みの license_id + user_id)
	type asgKey struct{ licenseID, userID int64 }
	seen := map[asgKey]int{}

	rr := newLicenseRefResolver(q)
	for _, r := range rows {
		lic, refErrs := rr.resolve(ctx, r)
		errs = append(errs, refErrs...)

		code := strings.TrimSpace(r.Fields["employee_code"])
		if code == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "従業員コードは必須です"})
			continue
		}
		u, err := q.GetUserByEmployeeCode(ctx, code)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "ユーザ '" + code + "' が見つかりません"})
			continue
		case err != nil:
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "lookup error: " + err.Error()})
			continue
		case u.DeactivatedAt != nil:
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "退職済みのユーザには割当できません"})
			continue
		}

		// 重複チェックはライセンスとユーザの双方が解決できた行のみ
		if len(refErrs) > 0 {
			continue
		}
		cnt, err := q.CountActiveUserAssignment(ctx, repository.CountActiveUserAssignmentParams{
			LicenseID: lic.ID, UserID: u.ID,
		})
		if err != nil {
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "lookup error: " + err.Error()})
			continue
		}
		if cnt > 0 {
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "既に割当済みです"})
		}
		k := asgKey{lic.ID, u.ID}
		if prev, ok := seen[k]; ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "employee_code", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
		} else {
			seen[k] = r.Line
		}
	}
	return errs
}

func (UserAssignmentsImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	rr := newLicenseRefResolver(q)
	for _, r := range rows {
		lic, refErrs := rr.resolve(ctx, r)
		if len(refErrs) > 0 {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve license: " + refErrs[0].Message)
		}
		u, err := q.GetUserByEmployeeCode(ctx, strings.TrimSpace(r.Fields["employee_code"]))
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve user: " + err.Error())
		}
		if _, err := q.CreateUserAssignment(ctx, repository.CreateUserAssignmentParams{
			LicenseID:         lic.ID,
			UserID:            u.ID,
			ExternalAccountID: nilIfEmpty(strings.TrimSpace(r.Fields["external_account_id"])),
			Note:              nilIfEmpty(r.Fields["note"]),
		}); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}
