package departments

import (
	"strconv"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// canEdit は license_manager 以上か判定する。
func canEdit(role middleware.Role) bool {
	switch role {
	case middleware.RoleLicenseManager,
		middleware.RoleDepartmentSecurityAdmin,
		middleware.RoleSystemAdmin:
		return true
	}
	return false
}

// formatDate は *time.Time を YYYY-MM-DD にする (nil は空文字)。
// list / show の valid_from / valid_to 表示用。
func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// sourceLabel は source 列の表示名。AD 同期 PR で 'ad' / 'csv' が
// 流入するまで実質 'manual' のみだが、表示ロジックは先に揃えておく。
func sourceLabel(v string) string {
	switch v {
	case "manual":
		return "手動"
	case "ad":
		return "AD"
	case "csv":
		return "CSV"
	default:
		return v
	}
}
