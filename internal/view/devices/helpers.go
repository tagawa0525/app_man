package devices

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

// formatDateTime は *time.Time を YYYY-MM-DD HH:MM にする (nil は空文字)。
// retired_at / last_seen_at の表示で共通利用。
func formatDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

// userLabel は主利用者列 / show リンク / form select で共通利用するラベル。
// 退職済みなら "田川太郎 (退職)"、現役なら "田川太郎" を返す。
// users 側の departmentLabel と書式が違う (廃止日ではなく単に「(退職)」)
// ため、初登場の今は devices 内に閉じる (共通化対象外)。
func userLabel(u repository.User) string {
	if u.DeactivatedAt != nil {
		return u.Name + " (退職)"
	}
	return u.Name
}

// derefString は *string を文字列に展開する (nil は空文字)。
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
