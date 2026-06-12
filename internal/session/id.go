// Package session はサーバ側セッションの永続化・ID 生成・Cookie ヘルパを提供する。
//
// 仕様書 §7.3 / §8.3 に従い、セッション ID は crypto/rand 32 byte を
// base64.RawURLEncoding でエンコードして 43 文字の URL-safe 文字列にする。
// CSRF token も同じ表現で発行し、ログイン成功時の ID 再発行 (固定攻撃対策)
// と CSRF middleware の session-bound 検証 (別 PR) で再利用する。
package session

import (
	"crypto/rand"
	"encoding/base64"
)

const randomByteLen = 32

// NewID は session ID として使う 32 byte 乱数を base64.RawURLEncoding で
// 文字列化して返す。長さは固定で 43 文字。
func NewID() (string, error) {
	return randomString()
}

// NewCSRFToken は CSRF token として使う 32 byte 乱数を base64.RawURLEncoding
// で文字列化して返す。長さは固定で 43 文字。session ID と同じ生成器を
// 共有するが、用途を分けるためにラッパを分離している。
func NewCSRFToken() (string, error) {
	return randomString()
}

func randomString() (string, error) {
	buf := make([]byte, randomByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
