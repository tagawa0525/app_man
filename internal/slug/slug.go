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
//
// 置換結果が "." / ".." になった場合も "_" を返す。slug は
// path.Join("licenses", vendor_slug, product_slug, license_slug) の
// パス成分として使われるため、".." を素通しすると licenses/ 配下から
// 脱出するパスが組み立てられてしまう (パストラバーサル防止)。
// "..." 以上はパス上特別な意味を持たないのでそのまま保持する。
func Slugify(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "_"
	}
	out := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		// 空白は unicode.IsSpace 全般 (全角スペース・NBSP 含む) を対象に
		// する。TrimSpace が Unicode 空白を落とすのと基準を揃えるため。
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, trimmed)
	if out == "." || out == ".." {
		return "_"
	}
	return out
}
