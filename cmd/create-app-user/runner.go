package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tagawa0525/app_man/internal/repository"
)

// runOptions は CLI flag を解釈した後の正規化済み引数。
type runOptions struct {
	configPath     string
	username       string
	role           string // create モードでのみ意味を持つ
	departmentCode string // create モード + system_admin 以外で必須
	notifyEmail    string // create モードでのみ意味を持つ。空欄可
	resetPassword  bool   // true なら reset モード、false なら create モード
}

// createUser は app_users INSERT と user_department_roles INSERT を
// 1 トランザクションで実行する。部分挿入 (app_users だけ残ってロールが
// 付与されない状態) を防ぐため、tx 内で 2 件続けて書き込み、どちらかが
// 失敗したら全件ロールバックする。
//
// 本関数は flag 検証・パスワード入力・lock 取得などはしない。それらは
// 上位の run() が完了させ、ここには検証済みの opts と bcrypt 済み hash
// だけが渡される前提。
//
// 戻り値の error は app_users / user_department_roles INSERT のどちらか
// に起因するエラーを wrap して返す。呼び出し側は exit code を決める。
func createUser(ctx context.Context, sqlDB *sql.DB, opts runOptions, passwordHash string) error {
	departmentID, err := resolveDepartmentID(ctx, repository.New(sqlDB), opts.role, opts.departmentCode)
	if err != nil {
		return err
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback は Commit 成功時には no-op になる (database/sql 仕様)。
	defer func() { _ = tx.Rollback() }()

	q := repository.New(sqlDB).WithTx(tx)

	hashPtr := &passwordHash
	notifyPtr := nullableString(opts.notifyEmail)

	user, err := q.CreateAppUser(ctx, repository.CreateAppUserParams{
		Username:     opts.username,
		PasswordHash: hashPtr,
		LinkedUserID: nil, // ローカル admin は linked_user_id NULL
		NotifyEmail:  notifyPtr,
		AuthType:     "local",
	})
	if err != nil {
		return fmt.Errorf("create app_user: %w", err)
	}

	if _, err := q.CreateUserDepartmentRole(ctx, repository.CreateUserDepartmentRoleParams{
		AppUserID:    user.ID,
		DepartmentID: departmentID,
		Role:         opts.role,
	}); err != nil {
		return fmt.Errorf("create user_department_role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// resolveDepartmentID は role と department-code から department_id を引く。
// system_admin は全社スコープのため code は無視し (nil, nil) を返す。
// それ以外の role で code が空ならエラー。code 指定があれば DB から引く。
//
// 廃止済み部署 (valid_to IS NOT NULL) は拒否する。運用上、廃止部署への
// ロール付与は事故である可能性が高い。
func resolveDepartmentID(ctx context.Context, q *repository.Queries, role, code string) (*int64, error) {
	if role == "system_admin" {
		return nil, nil
	}
	if code == "" {
		return nil, fmt.Errorf("--department-code is required for role %q", role)
	}
	dept, err := q.GetDepartmentByCode(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("department not found: %s", code)
		}
		return nil, fmt.Errorf("lookup department %q: %w", code, err)
	}
	if dept.ValidTo != nil {
		return nil, fmt.Errorf("department %s is retired (valid_to=%s)", code, dept.ValidTo.Format("2006-01-02"))
	}
	return &dept.ID, nil
}

// nullableString は空文字を NULL (= nil) として扱うヘルパ。
// notify_email や department_code の「未指定」を sqlc の *string に
// マッピングするときに使う。
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
