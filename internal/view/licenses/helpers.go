package licenses

import (
	"strconv"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
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
func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// formatCount は total_count の表示。NULL = 無制限契約。
func formatCount(c *int64) string {
	if c == nil {
		return "無制限"
	}
	return strconv.FormatInt(*c, 10)
}

// countUnitLabel は count_unit の表示名。
func countUnitLabel(v string) string {
	switch v {
	case "device":
		return "デバイス"
	case "user":
		return "ユーザ"
	default:
		return v
	}
}

// contractTypeLabel は contract_type の表示名。
func contractTypeLabel(v string) string {
	switch v {
	case "perpetual":
		return "永続"
	case "subscription":
		return "サブスクリプション"
	default:
		return v
	}
}

// derefOrEmpty は *string を string にする (nil なら空文字)。
func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// formatPrice は unit_price + currency の表示 (未入力は空文字)。
func formatPrice(price *int64, currency *string) string {
	if price == nil {
		return ""
	}
	s := strconv.FormatInt(*price, 10)
	if currency != nil && *currency != "" {
		s += " " + *currency
	}
	return s
}

// deviceOptionLabel は端末割当 select の表示名 (資産コード [ホスト名])。
func deviceOptionLabel(d repository.Device) string {
	label := d.AssetCode
	if d.Hostname != nil && *d.Hostname != "" {
		label += " (" + *d.Hostname + ")"
	}
	return label
}

// productLabel は product select の表示名 (ベンダー / 製品名 [エディション])。
func productLabel(p repository.ListProductsRow) string {
	label := p.VendorName + " / " + p.CanonicalName
	if p.Edition != nil && *p.Edition != "" {
		label += " (" + *p.Edition + ")"
	}
	return label
}
