// Package export は /admin/export (仕様 §5.10 / Plan admin-export.md) の
// エクスポート生成本体。web ハンドラから分離してテスト可能にしてある。
//
//   - WriteExcel: 業務データの正本 10 テーブルを 1 ブック 10 シートに書く
//   - WriteZip: DB スナップショット (VACUUM INTO) + licenses/ ツリーを書く
package export

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/tagawa0525/app_man/internal/repository"
)

// timeLayout はセルに書く日時の書式。DB (SQLite) の格納値は UTC の
// CURRENT_TIMESTAMP なので、タイムゾーンが明示される RFC3339 で出す。
const timeLayout = time.RFC3339

// sheet は 1 シート分の描画データ。rows の各要素は header と同順の列値。
type sheet struct {
	name   string
	header []string
	rows   [][]any
}

// WriteExcel は業務データの正本 10 テーブル (vendors / products /
// departments / users / devices / licenses / user_assignments /
// device_assignments / approvals / app_settings) を 1 シート 1 テーブルで
// w に xlsx として書く。全件エクスポートであり、論理削除済み
// (revoked_at / deactivated_at 等が入った) 行も含む。sessions /
// audit_logs は機微・肥大のため対象外 (Plan)。
//
// licenses の product_keys 列は includeKeys=true のときのみ存在する。
// 呼び出し側 (web ハンドラ) は includeKeys=true を audit_logs に記録して
// から呼ぶこと (記録なしの機微データ持ち出しを作らない)。
func WriteExcel(ctx context.Context, q *repository.Queries, w io.Writer, includeKeys bool) error {
	sheets, err := buildSheets(ctx, q, includeKeys)
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	// excelize の新規ブックは Sheet1 だけを持つ。先頭シートを rename し、
	// 2 枚目以降を追加していくと sheets の順がそのままシート順になる。
	if err := f.SetSheetName("Sheet1", sheets[0].name); err != nil {
		return fmt.Errorf("rename first sheet: %w", err)
	}
	for i, s := range sheets {
		if i > 0 {
			if _, err := f.NewSheet(s.name); err != nil {
				return fmt.Errorf("create sheet %s: %w", s.name, err)
			}
		}
		header := make([]any, len(s.header))
		for j, h := range s.header {
			header[j] = h
		}
		if err := f.SetSheetRow(s.name, "A1", &header); err != nil {
			return fmt.Errorf("write header of sheet %s: %w", s.name, err)
		}
		for ri := range s.rows {
			cell, err := excelize.CoordinatesToCellName(1, ri+2)
			if err != nil {
				return fmt.Errorf("cell name for sheet %s row %d: %w", s.name, ri+2, err)
			}
			if err := f.SetSheetRow(s.name, cell, &s.rows[ri]); err != nil {
				return fmt.Errorf("write sheet %s row %d: %w", s.name, ri+2, err)
			}
		}
	}

	if err := f.Write(w); err != nil {
		return fmt.Errorf("write xlsx: %w", err)
	}
	return nil
}

// buildSheets は 10 シート分のデータを Export* / ListAppSettings で集める。
func buildSheets(ctx context.Context, q *repository.Queries, includeKeys bool) ([]sheet, error) {
	vendors, err := q.ExportVendors(ctx)
	if err != nil {
		return nil, fmt.Errorf("export vendors: %w", err)
	}
	products, err := q.ExportProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("export products: %w", err)
	}
	departments, err := q.ExportDepartments(ctx)
	if err != nil {
		return nil, fmt.Errorf("export departments: %w", err)
	}
	users, err := q.ExportUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("export users: %w", err)
	}
	devices, err := q.ExportDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("export devices: %w", err)
	}
	licenses, err := q.ExportLicenses(ctx)
	if err != nil {
		return nil, fmt.Errorf("export licenses: %w", err)
	}
	userAssignments, err := q.ExportUserAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("export user_assignments: %w", err)
	}
	deviceAssignments, err := q.ExportDeviceAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("export device_assignments: %w", err)
	}
	approvals, err := q.ExportApprovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("export approvals: %w", err)
	}
	settings, err := q.ListAppSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("export app_settings: %w", err)
	}

	return []sheet{
		vendorSheet(vendors),
		productSheet(products),
		departmentSheet(departments),
		userSheet(users),
		deviceSheet(devices),
		licenseSheet(licenses, includeKeys),
		userAssignmentSheet(userAssignments),
		deviceAssignmentSheet(deviceAssignments),
		approvalSheet(approvals),
		appSettingSheet(settings),
	}, nil
}

// ヘッダは DB のカラム名をそのまま使う (日本語ラベルではなく)。
// スキーマとの対応が自明で、再取込・突合に使える形を優先する。

func vendorSheet(rows []repository.Vendor) sheet {
	s := sheet{
		name:   "vendors",
		header: []string{"id", "name", "url", "note", "created_at", "updated_at"},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(r.ID, r.Name, r.Url, r.Note, r.CreatedAt, r.UpdatedAt))
	}
	return s
}

func productSheet(rows []repository.Product) sheet {
	s := sheet{
		name: "products",
		header: []string{
			"id", "vendor_id", "canonical_name", "edition", "software_type",
			"license_required", "default_approval_status", "canonical_download_url",
			"service_admin_url", "license_terms_url", "note", "created_at", "updated_at",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.VendorID, r.CanonicalName, r.Edition, r.SoftwareType,
			r.LicenseRequired, r.DefaultApprovalStatus, r.CanonicalDownloadUrl,
			r.ServiceAdminUrl, r.LicenseTermsUrl, r.Note, r.CreatedAt, r.UpdatedAt,
		))
	}
	return s
}

func departmentSheet(rows []repository.Department) sheet {
	s := sheet{
		name: "departments",
		header: []string{
			"id", "code", "name", "parent_id", "successor_department_id",
			"valid_from", "valid_to", "source", "source_ou", "last_synced_at",
			"created_at", "updated_at",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.Code, r.Name, r.ParentID, r.SuccessorDepartmentID,
			r.ValidFrom, r.ValidTo, r.Source, r.SourceOu, r.LastSyncedAt,
			r.CreatedAt, r.UpdatedAt,
		))
	}
	return s
}

func userSheet(rows []repository.User) sheet {
	s := sheet{
		name: "users",
		header: []string{
			"id", "employee_code", "username", "name", "email", "department_id",
			"deactivated_at", "source", "source_dn", "ad_modified_at",
			"last_synced_at", "created_at", "updated_at",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.EmployeeCode, r.Username, r.Name, r.Email, r.DepartmentID,
			r.DeactivatedAt, r.Source, r.SourceDn, r.AdModifiedAt,
			r.LastSyncedAt, r.CreatedAt, r.UpdatedAt,
		))
	}
	return s
}

func deviceSheet(rows []repository.Device) sheet {
	s := sheet{
		name: "devices",
		header: []string{
			"id", "asset_code", "hostname", "primary_user_id", "department_id",
			"retired_at", "last_seen_at", "created_at", "updated_at",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.AssetCode, r.Hostname, r.PrimaryUserID, r.DepartmentID,
			r.RetiredAt, r.LastSeenAt, r.CreatedAt, r.UpdatedAt,
		))
	}
	return s
}

// licenseSheet は includeKeys=false のとき product_keys 列自体を出さない
// (空欄の列を残すと「キーが未登録」と読み違えるため、列ごと消す。Plan)。
func licenseSheet(rows []repository.License, includeKeys bool) sheet {
	s := sheet{
		name: "licenses",
		header: []string{
			"id", "product_id", "owning_department_id", "license_slug",
			"display_name", "total_count", "count_unit", "contract_type",
			"purchased_at", "started_at", "expires_at", "vendor_order_no",
			"purchaser", "unit_price", "currency", "fs_dir_path", "note",
			"created_at", "updated_at",
		},
	}
	if includeKeys {
		s.header = append(s.header, "product_keys")
	}
	for _, r := range rows {
		row := cells(
			r.ID, r.ProductID, r.OwningDepartmentID, r.LicenseSlug,
			r.DisplayName, r.TotalCount, r.CountUnit, r.ContractType,
			r.PurchasedAt, r.StartedAt, r.ExpiresAt, r.VendorOrderNo,
			r.Purchaser, r.UnitPrice, r.Currency, r.FsDirPath, r.Note,
			r.CreatedAt, r.UpdatedAt,
		)
		if includeKeys {
			row = append(row, cellValue(r.ProductKeys))
		}
		s.rows = append(s.rows, row)
	}
	return s
}

func userAssignmentSheet(rows []repository.UserAssignment) sheet {
	s := sheet{
		name: "user_assignments",
		header: []string{
			"id", "license_id", "user_id", "external_account_id",
			"provisioned_at", "deprovisioned_at", "assigned_at", "revoked_at", "note",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.LicenseID, r.UserID, r.ExternalAccountID,
			r.ProvisionedAt, r.DeprovisionedAt, r.AssignedAt, r.RevokedAt, r.Note,
		))
	}
	return s
}

func deviceAssignmentSheet(rows []repository.DeviceAssignment) sheet {
	s := sheet{
		name: "device_assignments",
		header: []string{
			"id", "license_id", "device_id", "assigned_at", "revoked_at", "note",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.LicenseID, r.DeviceID, r.AssignedAt, r.RevokedAt, r.Note,
		))
	}
	return s
}

func approvalSheet(rows []repository.DepartmentProductApproval) sheet {
	s := sheet{
		name: "approvals",
		header: []string{
			"id", "department_id", "product_id", "status", "scope_type",
			"conditions", "approved_by_app_user_id", "approved_at", "expires_at",
			"revoked_at", "revoked_by_app_user_id", "revoke_reason",
			"approval_source", "source_request_id", "note", "created_at", "updated_at",
		},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(
			r.ID, r.DepartmentID, r.ProductID, r.Status, r.ScopeType,
			r.Conditions, r.ApprovedByAppUserID, r.ApprovedAt, r.ExpiresAt,
			r.RevokedAt, r.RevokedByAppUserID, r.RevokeReason,
			r.ApprovalSource, r.SourceRequestID, r.Note, r.CreatedAt, r.UpdatedAt,
		))
	}
	return s
}

func appSettingSheet(rows []repository.AppSetting) sheet {
	s := sheet{
		name:   "app_settings",
		header: []string{"key", "value", "updated_at", "updated_by_app_user_id"},
	}
	for _, r := range rows {
		s.rows = append(s.rows, cells(r.Key, r.Value, r.UpdatedAt, r.UpdatedByAppUserID))
	}
	return s
}

// cells は列値を cellValue で変換した 1 行分のスライスにする。
func cells(vs ...any) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = cellValue(v)
	}
	return out
}

// cellValue は repository の行フィールドをセル値へ変換する。NULL 許容の
// ポインタ型は nil を空セルにし、日時は timeLayout の文字列にする
// (excelize に time.Time を渡すとシリアル値 + 書式になり、読み手の環境で
// 表示が揺れるため文字列で固定する)。
func cellValue(v any) any {
	switch x := v.(type) {
	case *string:
		if x == nil {
			return ""
		}
		return *x
	case *int64:
		if x == nil {
			return ""
		}
		return *x
	case *bool:
		if x == nil {
			return ""
		}
		return *x
	case time.Time:
		return x.Format(timeLayout)
	case *time.Time:
		if x == nil {
			return ""
		}
		return x.Format(timeLayout)
	default:
		return v
	}
}
