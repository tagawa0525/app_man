package users

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

// formatDateTime は *time.Time を YYYY-MM-DD HH:MM にする (nil は空文字)。
// deactivated_at 表示用。
func formatDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04")
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

// derefString は *string を文字列に展開する (nil は空文字)。
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
