// Package auth はパスワードハッシュ生成・検証のヘルパを提供する。
// bcrypt を用いたローカル認証用 (auth_type='local') のシステム管理
// アカウント向け。一般社員 (auth_type='ad') は AD パススルー認証で
// app_man 側に認証情報を持たないため、本パッケージは使用しない。
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultCost は bcrypt ハッシュ生成時のコストパラメータ。
	// bcrypt.DefaultCost (= 10) と同じだが、本パッケージ内で 1 箇所
	// 定義しておくことで将来チューニングを 1 行で済ませる。
	DefaultCost = bcrypt.DefaultCost

	// MinPasswordLength は受け入れ可能な最低パスワード長。
	// bcrypt 自体は 72 byte 上限のみで弱パスワード防止が無いため、
	// 自前で最低長を強制する。
	MinPasswordLength = 8
)

var (
	// ErrPasswordTooShort は MinPasswordLength 未満の plaintext を
	// Hash しようとした場合に返る。
	ErrPasswordTooShort = errors.New("password too short")

	// ErrPasswordMismatch は Verify でハッシュと plaintext が
	// 一致しなかった場合に返る (bcrypt の sentinel をラップ)。
	ErrPasswordMismatch = errors.New("password mismatch")
)

// Hash は plaintext を bcrypt cost=DefaultCost でハッシュ化する。
// MinPasswordLength 未満なら ErrPasswordTooShort を返す。
func Hash(plaintext string) (string, error) {
	if len(plaintext) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt generate: %w", err)
	}
	return string(b), nil
}

// Verify は hash と plaintext を bcrypt.CompareHashAndPassword で照合する。
// 不一致は ErrPasswordMismatch を返す。ハッシュ形式不正等の別エラーは
// bcrypt 由来のエラーをラップして返す。
func Verify(hash, plaintext string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrPasswordMismatch
	}
	return fmt.Errorf("bcrypt compare: %w", err)
}
