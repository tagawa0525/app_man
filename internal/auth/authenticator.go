package auth

import (
	"context"
	"errors"
)

// AuthenticatedUser はログインが成功した際に Authenticator が返すユーザ情報。
// DB レイヤの行表現を上のレイヤに漏らさないため、最小限の項目に絞る。
// 後続 PR で linked_user_id 等が必要になれば拡張する。
type AuthenticatedUser struct {
	ID       int64  // app_users.id
	Username string // app_users.username
}

// Authenticator は username / password を取り、認証に成功したら
// AuthenticatedUser を返す境界。仕様書 §7.3 で複数実装 (Local / LDAP /
// RemoteHeader / Composite) を想定して interface 化されている。
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*AuthenticatedUser, error)
}

// 認証失敗を表す sentinel エラー。ハンドラ側で表示文言を分岐させるために
// 用途を分けるが、ErrInvalidCredentials は「username 不在 / 誤パスワード /
// password_hash NULL」のすべてに使う (列挙攻撃対策)。
var (
	// ErrInvalidCredentials は認証情報が不正な場合のエラー。
	// 内訳は呼び出し元に漏らさない (username 存在の有無を返さない)。
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrUserDisabled は disabled_at が設定されたアカウントへのログイン試行で返る。
	// admin が UI で見て退職者の残存を発見する用途。
	ErrUserDisabled = errors.New("user disabled")

	// ErrUnsupportedAuthType は Authenticator がそのユーザの auth_type を
	// サポートしない場合に返る (例: LocalAuthenticator に auth_type='ad' が渡された)。
	// Composite 経路では拾って別 Authenticator に流す。
	ErrUnsupportedAuthType = errors.New("unsupported auth type")
)
