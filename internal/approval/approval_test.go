package approval_test

import (
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/approval"
)

// TestEvaluate は仕様 §5.5 の評価ロジック全分岐を検証する。
//
//  1. products.default_approval_status で即決する分岐
//     (globally_approved / globally_prohibited / unknown)。
//     department_discretion のみ 2 へ進む
//  2. アクティブ承認レコード (revoked_at IS NULL) の評価:
//     レコードなし / approved × scope_type 3 種 (specific_* は
//     scope に対象が含まれるか) / conditional / prohibited /
//     expires_at <= now は期限切れ (未承認扱い)
//
// Evaluate は純関数で、DB lookup は呼び出し側の責務。レコードの
// 有無は rec == nil、scope への対象包含は Record.InScope で渡す。
func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name          string
		defaultStatus string
		rec           *approval.Record
		want          approval.Verdict
	}{
		// --- 1. default_approval_status による即決 ---
		{
			name:          "globally_approved はレコードなしでも許可",
			defaultStatus: "globally_approved",
			rec:           nil,
			want:          approval.VerdictAllowed,
		},
		{
			// 全社許可は部署別レコードを見ない (仕様の手順 1 で確定)。
			name:          "globally_approved は部署別の禁止レコードより優先",
			defaultStatus: "globally_approved",
			rec:           &approval.Record{Status: "prohibited", ScopeType: "department_wide"},
			want:          approval.VerdictAllowed,
		},
		{
			name:          "globally_prohibited は禁止",
			defaultStatus: "globally_prohibited",
			rec:           nil,
			want:          approval.VerdictProhibited,
		},
		{
			name:          "globally_prohibited は部署別の承認レコードより優先",
			defaultStatus: "globally_prohibited",
			rec:           &approval.Record{Status: "approved", ScopeType: "department_wide"},
			want:          approval.VerdictProhibited,
		},
		{
			name:          "unknown は未審査",
			defaultStatus: "unknown",
			rec:           nil,
			want:          approval.VerdictUnreviewed,
		},
		{
			// DB に想定外の値が入っていた場合は「審査されていない」に
			// 倒す (安全側)。
			name:          "不明な default_approval_status は未審査に倒す",
			defaultStatus: "totally_bogus",
			rec:           nil,
			want:          approval.VerdictUnreviewed,
		},

		// --- 2. department_discretion: レコード評価 ---
		{
			name:          "レコードなしは未承認",
			defaultStatus: "department_discretion",
			rec:           nil,
			want:          approval.VerdictUnapproved,
		},
		{
			// department_wide では InScope を参照しない
			// (ゼロ値 false のままでも許可)。
			name:          "approved + department_wide は許可",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "department_wide"},
			want:          approval.VerdictAllowed,
		},
		{
			name:          "approved + specific_users で対象ユーザは許可",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "specific_users", InScope: true},
			want:          approval.VerdictAllowed,
		},
		{
			name:          "approved + specific_users で対象外ユーザは未承認",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "specific_users", InScope: false},
			want:          approval.VerdictUnapproved,
		},
		{
			name:          "approved + specific_devices で対象端末は許可",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "specific_devices", InScope: true},
			want:          approval.VerdictAllowed,
		},
		{
			name:          "approved + specific_devices で対象外端末は未承認",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "specific_devices", InScope: false},
			want:          approval.VerdictUnapproved,
		},
		{
			name:          "conditional は条件付き許可",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "conditional", ScopeType: "department_wide"},
			want:          approval.VerdictConditional,
		},
		{
			name:          "prohibited は禁止",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "prohibited", ScopeType: "department_wide"},
			want:          approval.VerdictProhibited,
		},
		{
			// DB に想定外の status が入っていた場合は未承認に倒す
			// (安全側)。
			name:          "不明な status は未承認に倒す",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "maybe", ScopeType: "department_wide"},
			want:          approval.VerdictUnapproved,
		},
		{
			name:          "approved + 不明な scope_type は未承認に倒す",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "galaxy_wide", InScope: true},
			want:          approval.VerdictUnapproved,
		},

		// --- 2. expires_at (期限切れ = 未承認扱い) ---
		{
			name:          "expires_at 経過は期限切れ",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "department_wide", ExpiresAt: &past},
			want:          approval.VerdictExpired,
		},
		{
			// 境界: 仕様は expires_at <= now() なので、ちょうど now は
			// 期限切れ。
			name:          "expires_at ちょうど now は期限切れ",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "department_wide", ExpiresAt: &now},
			want:          approval.VerdictExpired,
		},
		{
			name:          "expires_at が未来なら期限内 (許可)",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "department_wide", ExpiresAt: &future},
			want:          approval.VerdictAllowed,
		},
		{
			// 期限切れレコードは status に関わらず効力を失う
			// (仕様「期限切れ (未承認扱い)」)。
			name:          "conditional でも期限切れが優先",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "conditional", ScopeType: "department_wide", ExpiresAt: &past},
			want:          approval.VerdictExpired,
		},
		{
			name:          "prohibited でも期限切れが優先",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "prohibited", ScopeType: "department_wide", ExpiresAt: &past},
			want:          approval.VerdictExpired,
		},
		{
			name:          "specific_users で対象内でも期限切れが優先",
			defaultStatus: "department_discretion",
			rec:           &approval.Record{Status: "approved", ScopeType: "specific_users", InScope: true, ExpiresAt: &past},
			want:          approval.VerdictExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := approval.Evaluate(tt.defaultStatus, tt.rec, now); got != tt.want {
				t.Errorf("Evaluate(%q, %+v, now) = %q, want %q",
					tt.defaultStatus, tt.rec, got, tt.want)
			}
		})
	}
}

// TestVerdictValues は Verdict 定数の文字列表現を固定する。
// view 層の switch や監査ログ・集計での永続化に使われるため、
// 値の変更は互換性破壊になる。
func TestVerdictValues(t *testing.T) {
	tests := []struct {
		v    approval.Verdict
		want string
	}{
		{approval.VerdictAllowed, "allowed"},
		{approval.VerdictProhibited, "prohibited"},
		{approval.VerdictUnapproved, "unapproved"},
		{approval.VerdictUnreviewed, "unreviewed"},
		{approval.VerdictConditional, "conditional"},
		{approval.VerdictExpired, "expired"},
	}
	for _, tt := range tests {
		if string(tt.v) != tt.want {
			t.Errorf("Verdict = %q, want %q", string(tt.v), tt.want)
		}
	}
}
