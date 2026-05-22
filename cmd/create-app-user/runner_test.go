package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/auth"
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
	defer func() { _ = rows.Close() }()

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

// TestCreateUser_LicenseManager_WithDepartment は system_admin 以外の
// role で --department-code を解決し、その部署 ID で
// user_department_roles に INSERT されることを確認する。
func TestCreateUser_LicenseManager_WithDepartment(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	deptID := seedDepartment(t, sqlDB, "DEPT010", "営業部")

	opts := runOptions{
		username:       "sato_lm",
		role:           "license_manager",
		departmentCode: "DEPT010",
	}
	const passwordHash = "$2a$10$dummy.hash"

	if err := createUser(ctx, sqlDB, opts, passwordHash); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	user, err := repository.New(sqlDB).GetAppUserByUsername(ctx, "sato_lm")
	if err != nil {
		t.Fatalf("GetAppUserByUsername: %v", err)
	}

	roles := loadRolesForUser(t, sqlDB, user.ID)
	if len(roles) != 1 {
		t.Fatalf("role count: want 1, got %d", len(roles))
	}
	if roles[0].Role != "license_manager" {
		t.Errorf("role: want license_manager, got %q", roles[0].Role)
	}
	if roles[0].DepartmentID == nil || *roles[0].DepartmentID != deptID {
		t.Errorf("department_id: want %d, got %v", deptID, roles[0].DepartmentID)
	}
}

// TestResolveDepartmentID_RetiredDepartmentRejected は valid_to が
// 設定された (廃止済み) 部署を指定した場合に拒否されることを確認。
func TestResolveDepartmentID_RetiredDepartmentRejected(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	// valid_to を過去日付で挿入 (廃止済み)
	_, err := sqlDB.ExecContext(ctx,
		`INSERT INTO departments (code, name, valid_from, valid_to, source)
		 VALUES ('DEPT_OLD', '廃止部署', '2020-04-01', '2024-03-31', 'manual')`)
	if err != nil {
		t.Fatalf("seed retired dept: %v", err)
	}

	q := repository.New(sqlDB)
	_, err = resolveDepartmentID(ctx, q, "license_manager", "DEPT_OLD")
	if err == nil {
		t.Fatal("resolveDepartmentID with retired dept: want error, got nil")
	}
}

// TestResolveDepartmentID_NotFound は存在しない code を拒否することを確認。
func TestResolveDepartmentID_NotFound(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	q := repository.New(sqlDB)
	_, err := resolveDepartmentID(ctx, q, "license_manager", "NOSUCH")
	if err == nil {
		t.Fatal("resolveDepartmentID with unknown code: want error, got nil")
	}
}

// TestResolveDepartmentID_SystemAdmin_IgnoresCode は system_admin では
// code 指定に関わらず department_id=NULL (全社) を返すことを確認。
func TestResolveDepartmentID_SystemAdmin_IgnoresCode(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	q := repository.New(sqlDB)
	id, err := resolveDepartmentID(ctx, q, "system_admin", "")
	if err != nil {
		t.Fatalf("resolveDepartmentID system_admin empty code: %v", err)
	}
	if id != nil {
		t.Errorf("system_admin department_id: want nil, got %v", *id)
	}

	// code が渡されても system_admin は無視するべき
	id2, err := resolveDepartmentID(ctx, q, "system_admin", "DEPT010")
	if err != nil {
		t.Fatalf("resolveDepartmentID system_admin with code: %v", err)
	}
	if id2 != nil {
		t.Errorf("system_admin with code: want nil, got %v", *id2)
	}
}

// TestResetPassword_OverwritesHash は既存ユーザの password_hash を
// resetPassword が上書きすることを確認する。
func TestResetPassword_OverwritesHash(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	// 既存ユーザを seed
	opts := runOptions{
		username: "admin",
		role:     "system_admin",
	}
	if err := createUser(ctx, sqlDB, opts, "$2a$10$old.hash.value"); err != nil {
		t.Fatalf("seed createUser: %v", err)
	}

	const newHash = "$2a$10$new.hash.value"
	if err := resetPassword(ctx, sqlDB, "admin", newHash); err != nil {
		t.Fatalf("resetPassword: %v", err)
	}

	user, err := repository.New(sqlDB).GetAppUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetAppUserByUsername: %v", err)
	}
	if user.PasswordHash == nil || *user.PasswordHash != newHash {
		t.Errorf("password_hash: want %q, got %v", newHash, user.PasswordHash)
	}

	// roles は触らない
	roles := loadRolesForUser(t, sqlDB, user.ID)
	if len(roles) != 1 {
		t.Errorf("roles after reset: want 1 (unchanged), got %d", len(roles))
	}
}

// TestResetPassword_UserNotFound は存在しない username に対する reset が
// エラーになることを確認する。
func TestResetPassword_UserNotFound(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	err := resetPassword(ctx, sqlDB, "nosuch", "$2a$10$whatever")
	if err == nil {
		t.Fatal("resetPassword for missing user: want error, got nil")
	}
}

// TestCreateUser_DuplicateUsername は同じ username で 2 回 create すると
// 2 回目が UNIQUE 制約違反でエラーになることを確認する。
func TestCreateUser_DuplicateUsername(t *testing.T) {
	t.Parallel()

	sqlDB := handlertest.NewTestDB(t)
	ctx := context.Background()

	opts := runOptions{username: "dup", role: "system_admin"}
	if err := createUser(ctx, sqlDB, opts, "$2a$10$one"); err != nil {
		t.Fatalf("first createUser: %v", err)
	}
	err := createUser(ctx, sqlDB, opts, "$2a$10$two")
	if err == nil {
		t.Fatal("second createUser with same username: want error, got nil")
	}

	// 失敗時は user_department_roles に余計な行が残らないこと (rollback 確認)
	var roleCount int
	row := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_department_roles
		   WHERE app_user_id IN (SELECT id FROM app_users WHERE username = 'dup')`)
	if err := row.Scan(&roleCount); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	// 1 つ目の createUser が付けた 1 行のみが残るはず (2 回目は rollback で 0 行追加)
	if roleCount != 1 {
		t.Errorf("roles for dup user: want 1, got %d", roleCount)
	}
}

func TestValidateFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		opts    runOptions
		wantErr bool
	}{
		{"system_admin OK", runOptions{username: "a", role: "system_admin"}, false},
		{"license_manager with dept OK", runOptions{username: "a", role: "license_manager", departmentCode: "D"}, false},
		{"reset OK", runOptions{username: "a", resetPassword: true}, false},

		{"missing username", runOptions{role: "system_admin"}, true},
		{"reset missing username", runOptions{resetPassword: true}, true},
		{"unknown role", runOptions{username: "a", role: "wizard"}, true},
		{"license_manager without dept", runOptions{username: "a", role: "license_manager"}, true},
		{"viewer without dept", runOptions{username: "a", role: "viewer"}, true},
		{"general_user without dept", runOptions{username: "a", role: "general_user"}, true},
		{"create empty role", runOptions{username: "a"}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateFlags(tc.opts)
			if tc.wantErr && err == nil {
				t.Errorf("validateFlags(%+v): want error, got nil", tc.opts)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateFlags(%+v): want nil, got %v", tc.opts, err)
			}
		})
	}
}

func TestReadPassword_FromEnv(t *testing.T) {
	t.Parallel()

	stdin := &bytes.Buffer{} // env が優先されるので使われない
	stdout := &bytes.Buffer{}
	getenv := func(k string) string {
		if k == "APPMGR_INITIAL_PASSWORD" {
			return "FromEnvPass1"
		}
		return ""
	}

	pw, err := readPassword(stdin, stdout, getenv)
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if pw != "FromEnvPass1" {
		t.Errorf("password: want FromEnvPass1, got %q", pw)
	}
	if stdout.Len() != 0 {
		t.Errorf("env mode should not print prompt, got: %q", stdout.String())
	}
}

func TestReadPassword_EnvTooShort(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string {
		if k == "APPMGR_INITIAL_PASSWORD" {
			return "short" // 5 文字、MinPasswordLength=8 未満
		}
		return ""
	}

	_, err := readPassword(&bytes.Buffer{}, &bytes.Buffer{}, getenv)
	if !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("readPassword too-short env: want ErrPasswordTooShort, got %v", err)
	}
}

func TestReadPassword_FromStdinBuffer(t *testing.T) {
	t.Parallel()

	// stdin が *os.File でない場合 (テスト = bytes.Buffer) は
	// プロンプト 2 回 + 1 行ずつ読みで一致確認する設計。
	stdin := strings.NewReader("MyTestPass1\nMyTestPass1\n")
	stdout := &bytes.Buffer{}
	getenv := func(string) string { return "" } // env 未設定 → stdin 経路

	pw, err := readPassword(stdin, stdout, getenv)
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if pw != "MyTestPass1" {
		t.Errorf("password: want MyTestPass1, got %q", pw)
	}

	// プロンプトが 2 回出ているはず (Password: / Password (again):)
	if got := stdout.String(); !strings.Contains(got, "Password:") || !strings.Contains(got, "again") {
		t.Errorf("prompts not shown twice, got: %q", got)
	}
}

func TestReadPassword_StdinMismatch(t *testing.T) {
	t.Parallel()

	stdin := strings.NewReader("MyTestPass1\nDifferent2\n")
	getenv := func(string) string { return "" }

	_, err := readPassword(stdin, &bytes.Buffer{}, getenv)
	if err == nil {
		t.Fatal("readPassword mismatch: want error, got nil")
	}
	if !strings.Contains(err.Error(), "match") && !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected mismatch error, got: %v", err)
	}
}

func TestReadPassword_StdinTooShort(t *testing.T) {
	t.Parallel()

	stdin := strings.NewReader("abc\nabc\n")
	getenv := func(string) string { return "" }

	_, err := readPassword(stdin, &bytes.Buffer{}, getenv)
	if !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("readPassword stdin too-short: want ErrPasswordTooShort, got %v", err)
	}
}

// seedDepartment は active な部署を 1 件挿入して id を返すテスト用 helper。
func seedDepartment(t *testing.T, sqlDB *sql.DB, code, name string) int64 {
	t.Helper()
	res, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO departments (code, name, valid_from, source)
		 VALUES (?, ?, '2020-04-01', 'manual')`,
		code, name)
	if err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}
