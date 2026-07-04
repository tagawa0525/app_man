// Package slug は仕様 §3.2 の規則で文字列をファイルシステム安全な
// slug に正規化する。日本語等の非 ASCII は保持し (Windows ファイル
// システムは Unicode 対応)、Windows で使えない文字と制御文字と
// スペースを _ に置換する。
//
// 衝突時のサフィックス付与 (_2, _3, ...) は物理ディレクトリの実在
// チェックと不可分のため、本パッケージでは扱わない (L-3 の責務)。
package slug

import (
	"strings"
	"unicode"
)

// Slugify は s を仕様 §3.2 の規則で slug に正規化する。
// 前後の空白を trim した後、禁止文字 (/ \ : * ? " < > |)・
// 制御文字・スペースを _ に置換する。結果が空文字になった場合は
// パス成分の欠落を防ぐため "_" を返す。
func Slugify(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, trimmed)
}
