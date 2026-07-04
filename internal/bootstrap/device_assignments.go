package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tagawa0525/app_man/internal/repository"
)

// DeviceAssignmentsImporter は device_assignments テーブルへの CSV 一括投入。
// CSV ヘッダ: vendor_name,product_name,edition,department_code,license_slug,
// asset_code,note
//
//   - 先頭 5 列はライセンス参照 (resolveLicenseRef で自然キー解決)
//   - asset_code 必須。退役端末 (retired_at NOT NULL) への割当は
//     検証エラー (web の 400 と同基準)
//   - DB のアクティブ割当・CSV 内の重複は検証エラー (web の 409 と同基準。
//     000006 の部分 UNIQUE uniq_device_assignments_active が最終防衛)
//   - note 任意 (空欄は NULL)
type DeviceAssignmentsImporter struct{}

func (DeviceAssignmentsImporter) Kind() string { return "device_assignments" }
func (DeviceAssignmentsImporter) HeaderColumns() []string {
	return []string{
		"vendor_name", "product_name", "edition", "department_code",
		"license_slug", "asset_code", "note",
	}
}

func (DeviceAssignmentsImporter) Validate(ctx context.Context, q *repository.Queries, rows []Row) []ValidationError {
	var errs []ValidationError

	// CSV 内重複検出 (解決済みの license_id + device_id)
	type asgKey struct{ licenseID, deviceID int64 }
	seen := map[asgKey]int{}

	for _, r := range rows {
		lic, refErrs := resolveLicenseRef(ctx, q, r)
		errs = append(errs, refErrs...)

		code := strings.TrimSpace(r.Fields["asset_code"])
		if code == "" {
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "資産コードは必須です"})
			continue
		}
		d, err := q.GetDeviceByAssetCode(ctx, code)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "端末 '" + code + "' が見つかりません"})
			continue
		case err != nil:
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "lookup error: " + err.Error()})
			continue
		case d.RetiredAt != nil:
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "退役済みの端末には割当できません"})
			continue
		}

		// 重複チェックはライセンスと端末の双方が解決できた行のみ
		if len(refErrs) > 0 {
			continue
		}
		cnt, err := q.CountActiveDeviceAssignment(ctx, repository.CountActiveDeviceAssignmentParams{
			LicenseID: lic.ID, DeviceID: d.ID,
		})
		if err != nil {
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "lookup error: " + err.Error()})
			continue
		}
		if cnt > 0 {
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "既に割当済みです"})
		}
		k := asgKey{lic.ID, d.ID}
		if prev, ok := seen[k]; ok {
			errs = append(errs, ValidationError{Line: r.Line, Column: "asset_code", Message: "CSV 内で重複しています (line " + itoa(prev) + ")"})
		} else {
			seen[k] = r.Line
		}
	}
	return errs
}

func (DeviceAssignmentsImporter) Insert(ctx context.Context, q *repository.Queries, rows []Row) (int, error) {
	for _, r := range rows {
		lic, refErrs := resolveLicenseRef(ctx, q, r)
		if len(refErrs) > 0 {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve license: " + refErrs[0].Message)
		}
		d, err := q.GetDeviceByAssetCode(ctx, strings.TrimSpace(r.Fields["asset_code"]))
		if err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": resolve device: " + err.Error())
		}
		if _, err := q.CreateDeviceAssignment(ctx, repository.CreateDeviceAssignmentParams{
			LicenseID: lic.ID,
			DeviceID:  d.ID,
			Note:      nilIfEmpty(r.Fields["note"]),
		}); err != nil {
			return 0, errors.New("line " + itoa(r.Line) + ": " + err.Error())
		}
	}
	return len(rows), nil
}
