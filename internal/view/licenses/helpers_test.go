package licenses

import "testing"

// formatSizeBytes は units の範囲を超える巨大値でも panic せず
// 最大単位 (TiB) にクランプして表示する。
func TestFormatSizeBytes(t *testing.T) {
	const kib = int64(1024)
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{"バイトはそのまま", 512, "512 B"},
		{"KiB", 2 * kib, "2.0 KiB"},
		{"MiB", 20 * kib * kib, "20.0 MiB"},
		{"TiB", 3 * kib * kib * kib * kib, "3.0 TiB"},
		{"1 PiB は TiB にクランプ (panic しない)", kib * kib * kib * kib * kib, "1024.0 TiB"},
		{"int64 最大値も panic しない", 1<<63 - 1, "8388608.0 TiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSizeBytes(tt.in); got != tt.want {
				t.Errorf("formatSizeBytes(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
