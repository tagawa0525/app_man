package appusers

import "strconv"

// itoa は int64 → 10 進文字列。URL やロール数の表示組み立てに使う。
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
