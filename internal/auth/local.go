package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tagawa0525/app_man/internal/repository"
)

// LocalAuthenticator は app_users テーブルの bcrypt ハッシュで認証する
// 実装。`auth_type='local'` のレコードのみ対象とし、他の auth_type は
// ErrUnsupportedAuthType で拒否する (Composite から再振り分けされる前提)。
type LocalAuthenticator struct {
	q *repository.Queries
}

// NewLocalAuthenticator は *sql.DB を受け取り LocalAuthenticator を組み立てる。
func NewLocalAuthenticator(db *sql.DB) *LocalAuthenticator {
	return &LocalAuthenticator{q: repository.New(db)}
}

// Authenticate は username / password を検証する。
//
// 戻り値のエラー種別:
//   - ErrInvalidCredentials: 未存在 username、誤パスワード、password_hash NULL
//   - ErrUserDisabled: disabled_at IS NOT NULL
//   - ErrUnsupportedAuthType: auth_type != 'local'
//   - その他: DB lookup や bcrypt 内部エラーのラップ
func (a *LocalAuthenticator) Authenticate(ctx context.Context, username, password string) (*AuthenticatedUser, error) {
	row, err := a.q.GetAppUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup app user: %w", err)
	}

	if row.AuthType != "local" {
		return nil, ErrUnsupportedAuthType
	}
	if row.DisabledAt != nil {
		return nil, ErrUserDisabled
	}
	if row.PasswordHash == nil {
		// auth_type='local' なのに password_hash が NULL は通常起きないが、
		// データ不整合に備えて ErrInvalidCredentials で拒否する。
		return nil, ErrInvalidCredentials
	}

	if err := Verify(*row.PasswordHash, password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("verify password: %w", err)
	}

	return &AuthenticatedUser{ID: row.ID, Username: row.Username}, nil
}
