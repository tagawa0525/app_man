// Package dashboard は GET / (ダッシュボード最小版、Plan
// dashboard-minimal.md) の templ ビュー。表示するのは現存データ源で
// 意味を持つ 4 ウィジェットのみで、SKYSEA / AD / 棚卸し等に依存する
// ウィジェットのプレースホルダは置かない (空枠は運用者を混乱させる)。
package dashboard

import (
	"strconv"
	"time"

	"github.com/tagawa0525/app_man/internal/approval"
	"github.com/tagawa0525/app_man/internal/repository"
)

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// assignedTotal はユーザ・端末両割当数の合計。v_license_usage は製品
// 単位の集計で count_unit を持たない (同一製品に user 契約と device
// 契約が混在しうる) ため、単位を問わず合計を「割当」として表示する。
func assignedTotal(u repository.ListLicenseUsageRow) int64 {
	return u.UserAssignedCount + u.DeviceAssignedCount
}

// ownedLabel は保有数の表示。無制限 (total_count NULL) のライセンスは
// v_license_usage の total_owned に 0 として畳まれるため、has_unlimited
// で補って「無制限」を明示する (0 と紛れさせない)。
func ownedLabel(u repository.ListLicenseUsageRow) string {
	if u.HasUnlimited {
		if u.TotalOwned > 0 {
			return itoa(u.TotalOwned) + " + 無制限"
		}
		return "無制限"
	}
	return itoa(u.TotalOwned)
}

// overAllocated は超過バッジの表示条件 (割当合計 > 保有)。無制限を
// 含む製品は保有上限が無いため警告しない (Plan 受け入れ基準
// 「total_count NULL の製品が不足扱いにならない」)。
func overAllocated(u repository.ListLicenseUsageRow) bool {
	return !u.HasUnlimited && assignedTotal(u) > u.TotalOwned
}

// diffLabel は過不足列の値 (保有 - 割当合計)。無制限を含む製品と、
// 保有 0 かつ割当 0 の製品 (マスタ登録のみ) は "-" 表示。超過時の
// 警告表示はテンプレ側 (overAllocated) が担う。
func diffLabel(u repository.ListLicenseUsageRow) string {
	if u.HasUnlimited {
		return "-"
	}
	a := assignedTotal(u)
	if u.TotalOwned == 0 && a == 0 {
		return "-"
	}
	return itoa(u.TotalOwned - a)
}

// defaultStatusLabel は products.default_approval_status の日本語表示。
// 未知の値はそのまま表示する (握りつぶさず目に見える形で出す)。
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

// fmtDatePtr は *time.Time を YYYY-MM-DD で表示する (nil は "-")。
func fmtDatePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format("2006-01-02")
}

// fmtDate は time.Time を YYYY-MM-DD で表示する。
func fmtDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// deptIDLabel は users.department_id (NULL 許容) の表示。部署名 JOIN は
// 仕様 §5.14 の SQL をそのまま使う決定に合わせて行わない。
func deptIDLabel(p *int64) string {
	if p == nil {
		return "-"
	}
	return itoa(*p)
}
