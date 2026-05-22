package bootstrap

import "strconv"

// itoa は int → string の薄いラッパ。エラーメッセージで頻用するため。
func itoa(n int) string {
	return strconv.Itoa(n)
}

// nilIfEmpty は空文字列を nil に変換し、それ以外は元の文字列ポインタを返す。
// nullable な TEXT 列の INSERT で空欄を NULL にするため。
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
