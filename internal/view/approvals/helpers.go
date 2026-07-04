// Package approvals は承認管理 3 画面 (仕様 §6.1) の templ ビュー。
// Verdict / status の日本語化は本パッケージ (表示側) の責務で、
// internal/approval は定数のまま返す (Plan approvals.md)。
package approvals

import (
	"strconv"
	"time"

	"github.com/tagawa0525/app_man/internal/approval"
)

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// detailPath は登録・編集画面のパス (/approvals/{deptID}/{productID})。
func detailPath(deptID, productID int64) string {
	return "/approvals/" + itoa(deptID) + "/" + itoa(productID)
}

// VerdictLabel は approval.Evaluate の Verdict を画面表示用の日本語にする。
// 未知の値はそのまま表示する (握りつぶさず目に見える形で出す)。
func VerdictLabel(v approval.Verdict) string {
	switch v {
	case approval.VerdictAllowed:
		return "許可"
	case approval.VerdictProhibited:
		return "禁止"
	case approval.VerdictUnapproved:
		return "未承認"
	case approval.VerdictUnreviewed:
		return "未審査"
	case approval.VerdictConditional:
		return "条件付き"
	case approval.VerdictExpired:
		return "期限切れ"
	}
	return string(v)
}

// defaultStatusLabel は products.default_approval_status の日本語表示。
func defaultStatusLabel(s string) string {
	switch s {
	case approval.DefaultGloballyApproved:
		return "全社許可"
	case approval.DefaultGloballyProhibited:
		return "全社禁止"
	case approval.DefaultUnknown:
		return "未審査"
	case approval.DefaultDepartmentDiscretion:
		return "部署裁量"
	}
	return s
}

// statusLabel は department_product_approvals.status の日本語表示。
func statusLabel(s string) string {
	switch s {
	case approval.StatusApproved:
		return "承認"
	case approval.StatusConditional:
		return "条件付き承認"
	case approval.StatusProhibited:
		return "禁止"
	}
	return s
}

// fmtDatePtr は *time.Time を YYYY-MM-DD で表示する (nil は "-")。
func fmtDatePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format("2006-01-02")
}

// fmtDateTimePtr は *time.Time を分単位で表示する (nil は "-")。
func fmtDateTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

// deref は *string を表示用 string にする (nil は "-")。
func deref(p *string) string {
	if p == nil || *p == "" {
		return "-"
	}
	return *p
}
