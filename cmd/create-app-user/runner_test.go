package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// TestCreateUser_SystemAdmin はもっとも単純な「system_admin を作る」
// シナリオで、app_users と user_department_roles に各 1 行が
// INSERT されることを検証する。
//
// system_admin は全社スコープのため department_id IS NULL。
func TestCreateUser_SystemAdmin(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	notifyEmail := "admin@example.com"
	opts := runOptions{
		username:    "admin",
		role:        "system_admin",
		notifyEmail: notifyEmail,
	}
	// bcrypt の本物ハッシュは internal/auth_test で別途検証済み。
	// ここでは「createUser が tx で 2 表に書き込む」ことだけ確認。
	const passwordHash = "$2a$10$dummy.hash.for.test.only"

	if err := createUser(ctx, sqlDB, opts, passwordHash); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	q := repository.New(sqlDB)

	user, err := q.GetAppUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetAppUserByUsername: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("username: want admin, got %q", user.Username)
	}
	if user.AuthType != "local" {
		t.Errorf("auth_type: want local, got %q", user.AuthType)
	}
	if user.PasswordHash == nil || *user.PasswordHash != passwordHash {
		t.Errorf("password_hash mismatch: %v", user.PasswordHash)
	}
	if user.NotifyEmail == nil || *user.NotifyEmail != notifyEmail {
		t.Errorf("notify_email: want %q, got %v", notifyEmail, user.NotifyEmail)
	}
	if user.LinkedUserID != nil {
		t.Errorf("linked_user_id: want nil, got %v", *user.LinkedUserID)
	}

	roles := loadRolesForUser(t, sqlDB, user.ID)
	if len(roles) != 1 {
		t.Fatalf("user_department_roles count: want 1, got %d", len(roles))
	}
	r := roles[0]
	if r.Role != "system_admin" {
		t.Errorf("role: want system_admin, got %q", r.Role)
	}
	if r.DepartmentID != nil {
		t.Errorf("department_id: want NULL (全社), got %v", *r.DepartmentID)
	}
}

// loadRolesForUser は app_user_id に紐づく active な role 行を返す。
// 専用クエリはまだ無いので raw SQL で取る (UI PR で sqlc 化予定)。
func loadRolesForUser(t *testing.T, sqlDB *sql.DB, appUserID int64) []roleRow {
	t.Helper()
	rows, err := sqlDB.QueryContext(context.Background(),
		`SELECT id, app_user_id, department_id, role
		   FROM user_department_roles
		  WHERE app_user_id = ? AND revoked_at IS NULL`,
		appUserID)
	if err != nil {
		t.Fatalf("query roles: %v", err)
	}
	defer rows.Close()

	var result []roleRow
	for rows.Next() {
		var r roleRow
		if err := rows.Scan(&r.ID, &r.AppUserID, &r.DepartmentID, &r.Role); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return result
}

type roleRow struct {
	ID           int64
	AppUserID    int64
	DepartmentID *int64
	Role         string
}
