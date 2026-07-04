package slug

import "testing"

// TestSlugify は仕様 §3.2 の slug 生成規則を検証する:
// 禁止文字 (/ \ : * ? " < > |)・制御文字・スペースを _ に置換し、
// 日本語等の非 ASCII は保持する。前後空白は trim し、空になったら "_"。
func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"スペースはアンダースコアに", "Adobe Acrobat Pro", "Adobe_Acrobat_Pro"},
		{"スラッシュとコロン", "A/B:C", "A_B_C"},
		{"禁止文字を全て置換", `a\b*c?d"e<f>g|h`, "a_b_c_d_e_f_g_h"},
		{"日本語は保持しスペースのみ置換", "契約 2024-04 営業部", "契約_2024-04_営業部"},
		{"制御文字 NUL", "a\x00b", "a_b"},
		{"制御文字 改行", "a\nb", "a_b"},
		{"制御文字 タブ", "a\tb", "a_b"},
		{"前後空白は trim", " x ", "x"},
		{"空文字はアンダースコア", "", "_"},
		{"空白のみはアンダースコア", "  ", "_"},
		{"禁止文字のみは全て置換", "///", "___"},
		{"ハイフン・アンダースコア・英数字はそのまま", "abc-DEF_123", "abc-DEF_123"},
		// "." / ".." が slug としてそのまま残ると
		// path.Join("licenses", slug, ...) がディレクトリ脱出になるため
		// "_" に潰す。3 ドット以上はパス的に無害なのでそのまま。
		{"ドット単独はアンダースコア", ".", "_"},
		{"ドット2つはアンダースコア", "..", "_"},
		{"trim 後にドット2つはアンダースコア", " .. ", "_"},
		{"ドット3つはそのまま", "...", "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.in); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
