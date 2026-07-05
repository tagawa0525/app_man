package repository_test

import (
	"context"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// user_department_roles_test.go は RevokeUserDepartmentRole の last-admin
// ガードを repository レベルで固定する (PR #35 Copilot 指摘)。
//
// handler 側の COUNT → UPDATE の 2 手だと、WAL の並行 write tx がそれぞれ
// tx 開始時スナップショットで COUNT=2 を観測し、両方の剥奪が通って有効な
// system_admin が 0 人になり得る。ガードを UPDATE の WHERE に埋め込めば
// 判定は UPDATE 実行時 (書込みロック下) に評価され原子的になる:
//
//   - 対象行が system_admin ロールで、「アクティブ system_admin ロールを
//     持つ有効 (disabled_at IS NULL) ユーザ」が 1 人以下なら 0 行更新
//   - system_admin 以外のロールはガード対象外 (常に剥奪可)

// seedAppUserWithRole は app_user と role 行を 1 つずつ作る。
func seedAppUserWithRole(t *testing.T, q *repository.Queries, username, role string, disabled bool) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	hash := ""
	u, err := q.CreateAppUser(ctx, repository.CreateAppUserParams{
		Username:     username,
		PasswordHash: &hash,
		AuthType:     "local",
	})
	if err != nil {
		t.Fatalf("CreateAppUser(%s): %v", username, err)
	}
	r, err := q.CreateUserDepartmentRole(ctx, repository.CreateUserDepartmentRoleParams{
		AppUserID: u.ID,
		Role:      role,
	})
	if err != nil {
		t.Fatalf("CreateUserDepartmentRole(%s, %s): %v", username, role, err)
	}
	if disabled {
		if _, err := q.DisableAppUser(ctx, u.ID); err != nil {
			t.Fatalf("DisableAppUser(%s): %v", username, err)
		}
	}
	return u.ID, r.ID
}

// revoke は RevokeUserDepartmentRole を実行して更新行数を返す。
func revoke(t *testing.T, q *repository.Queries, appUserID, roleID int64) int64 {
	t.Helper()
	n, err := q.RevokeUserDepartmentRole(context.Background(), repository.RevokeUserDepartmentRoleParams{
		ID:        roleID,
		AppUserID: appUserID,
	})
	if err != nil {
		t.Fatalf("RevokeUserDepartmentRole(%d, %d): %v", appUserID, roleID, err)
	}
	return n
}

// 唯一の有効 system_admin の剥奪は UPDATE 自体が 0 行になる (ガードが
// UPDATE 実行時に評価されるため、事前 COUNT とのレースが存在しない)。
func TestRevokeUserDepartmentRole_GuardsLastActiveSystemAdmin(t *testing.T) {
	t.Parallel()
	q := repository.New(handlertest.NewTestDB(t))

	adminID, adminRoleID := seedAppUserWithRole(t, q, "only_admin", "system_admin", false)

	if n := revoke(t, q, adminID, adminRoleID); n != 0 {
		t.Fatalf("revoke last active system_admin: affected = %d, want 0 (ガードで弾く)", n)
	}
	row, err := q.GetUserDepartmentRole(context.Background(), adminRoleID)
	if err != nil {
		t.Fatalf("GetUserDepartmentRole: %v", err)
	}
	if row.RevokedAt != nil {
		t.Error("revoked_at is set, want nil (最後の有効 admin は剥奪されない)")
	}
}

// 有効な system_admin が 2 人いれば 1 人目は剥奪でき、残った最後の
// 1 人は剥奪できない。
func TestRevokeUserDepartmentRole_AllowsWhenAnotherActiveAdminExists(t *testing.T) {
	t.Parallel()
	q := repository.New(handlertest.NewTestDB(t))

	firstID, firstRoleID := seedAppUserWithRole(t, q, "admin_a", "system_admin", false)
	secondID, secondRoleID := seedAppUserWithRole(t, q, "admin_b", "system_admin", false)

	if n := revoke(t, q, firstID, firstRoleID); n != 1 {
		t.Fatalf("revoke first admin (2 人目あり): affected = %d, want 1", n)
	}
	if n := revoke(t, q, secondID, secondRoleID); n != 0 {
		t.Fatalf("revoke second admin (最後の 1 人): affected = %d, want 0", n)
	}
}

// 無効化済みユーザの system_admin は「有効な admin」の勘定に入らない:
// 有効 admin が 1 人しかいなければ、無効化ユーザ側の行も剥奪できない
// (勘定が 1 のままのため)。有効 admin を足せば剥奪できる。
func TestRevokeUserDepartmentRole_ExcludesDisabledAdminsFromCount(t *testing.T) {
	t.Parallel()
	q := repository.New(handlertest.NewTestDB(t))

	_, _ = seedAppUserWithRole(t, q, "live_admin", "system_admin", false)
	frozenID, frozenRoleID := seedAppUserWithRole(t, q, "frozen_admin", "system_admin", true)

	if n := revoke(t, q, frozenID, frozenRoleID); n != 0 {
		t.Fatalf("revoke disabled admin (有効 admin 1 人): affected = %d, want 0", n)
	}

	_, _ = seedAppUserWithRole(t, q, "second_live_admin", "system_admin", false)
	if n := revoke(t, q, frozenID, frozenRoleID); n != 1 {
		t.Fatalf("revoke disabled admin (有効 admin 2 人): affected = %d, want 1", n)
	}
}

// system_admin 以外のロールはガード対象外: 有効 admin が 1 人しか
// いなくても剥奪できる。
func TestRevokeUserDepartmentRole_NonSystemAdminNotGuarded(t *testing.T) {
	t.Parallel()
	q := repository.New(handlertest.NewTestDB(t))

	_, _ = seedAppUserWithRole(t, q, "solo_admin", "system_admin", false)
	viewerID, viewerRoleID := seedAppUserWithRole(t, q, "plain_viewer", "viewer", false)

	if n := revoke(t, q, viewerID, viewerRoleID); n != 1 {
		t.Fatalf("revoke viewer role: affected = %d, want 1 (ガード対象外)", n)
	}
}
