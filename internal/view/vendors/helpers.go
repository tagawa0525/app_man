package vendors

import "strconv"

// itoa は int64 を 10 進文字列にする小さなヘルパ。
// templ から URL や ID 表示のために頻繁に呼ぶため、テンプレ内で
// strconv.FormatInt を直接書かない形に切り出す。
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
