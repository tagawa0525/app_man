// Package approval は仕様 §5.5 の承認評価ロジックを純関数で提供する。
// DB lookup (products.default_approval_status の取得、アクティブ承認
// レコードの取得、approval_scope_users / approval_scope_devices の
// 存在確認) は呼び出し側の責務で、本パッケージは値だけを受け取って
// 判定する。承認一覧画面のほか、後続のダッシュボード / SKYSEA 突合 /
// セルフサービスも同じ Evaluate を使う。
//
// 結果は Verdict 定数に留め、文言の日本語化は表示側の責務。
package approval

import "time"

// Verdict は評価結果。仕様 §5.5 の
// 許可 / 禁止 / 未承認 / 未審査 / 条件付き許可 / 期限切れ (未承認扱い)
// に対応する。expired は unapproved の亜種だが、表示で区別するため
// 別値にしている。
type Verdict string

const (
	// VerdictAllowed: 許可。
	VerdictAllowed Verdict = "allowed"
	// VerdictProhibited: 禁止。
	VerdictProhibited Verdict = "prohibited"
	// VerdictUnapproved: 未承認 (department_discretion で有効な承認がない)。
	VerdictUnapproved Verdict = "unapproved"
	// VerdictUnreviewed: 未審査 (default_approval_status = 'unknown')。
	VerdictUnreviewed Verdict = "unreviewed"
	// VerdictConditional: 条件付き許可 (人手確認推奨)。
	VerdictConditional Verdict = "conditional"
	// VerdictExpired: 期限切れ (未承認扱い)。
	VerdictExpired Verdict = "expired"
)

// products.default_approval_status の値。
const (
	DefaultGloballyApproved     = "globally_approved"
	DefaultGloballyProhibited   = "globally_prohibited"
	DefaultUnknown              = "unknown"
	DefaultDepartmentDiscretion = "department_discretion"
)

// department_product_approvals.status の値。
const (
	StatusApproved    = "approved"
	StatusConditional = "conditional"
	StatusProhibited  = "prohibited"
)

// department_product_approvals.scope_type の値。
const (
	ScopeDepartmentWide  = "department_wide"
	ScopeSpecificUsers   = "specific_users"
	ScopeSpecificDevices = "specific_devices"
)

// Record はアクティブな承認レコード (revoked_at IS NULL) のうち
// 評価に必要な値。DB の行そのものではなく、呼び出し側が
// repository の値から詰め替えて渡す。
type Record struct {
	// Status は approved / conditional / prohibited。
	Status string
	// ScopeType は department_wide / specific_users / specific_devices。
	ScopeType string
	// ExpiresAt は承認の有効期限。nil なら無期限。
	ExpiresAt *time.Time
	// InScope は specific_users / specific_devices のとき、評価対象の
	// user / device が approval_scope_users / approval_scope_devices に
	// 含まれるか。department_wide では参照しない。
	InScope bool
}

// Evaluate は仕様 §5.5 の手順で承認状態を評価する。
//
//  1. defaultStatus (products.default_approval_status) で即決:
//     globally_approved → 許可、globally_prohibited → 禁止、
//     unknown → 未審査。department_discretion のみ 2 へ。
//     想定外の値は未審査に倒す (審査されていないものを許可しない)
//  2. rec (アクティブ承認レコード、なければ nil) を評価:
//     nil → 未承認。expires_at <= now → 期限切れ (status に関わらず
//     効力を失う)。approved は scope_type と InScope で許可 / 未承認、
//     conditional → 条件付き許可、prohibited → 禁止。
//     想定外の status / scope_type は未承認に倒す
func Evaluate(defaultStatus string, rec *Record, now time.Time) Verdict {
	switch defaultStatus {
	case DefaultGloballyApproved:
		return VerdictAllowed
	case DefaultGloballyProhibited:
		return VerdictProhibited
	case DefaultDepartmentDiscretion:
		// 部署別レコードの評価へ。
	default:
		// unknown および想定外の値。
		return VerdictUnreviewed
	}

	if rec == nil {
		return VerdictUnapproved
	}
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now) {
		return VerdictExpired
	}
	switch rec.Status {
	case StatusApproved:
		switch rec.ScopeType {
		case ScopeDepartmentWide:
			return VerdictAllowed
		case ScopeSpecificUsers, ScopeSpecificDevices:
			if rec.InScope {
				return VerdictAllowed
			}
			return VerdictUnapproved
		default:
			return VerdictUnapproved
		}
	case StatusConditional:
		return VerdictConditional
	case StatusProhibited:
		return VerdictProhibited
	default:
		return VerdictUnapproved
	}
}
