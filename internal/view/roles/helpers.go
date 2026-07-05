package roles

import "strconv"

// itoa は int64 → 10 進文字列。URL 組み立てに使う。
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
