package static

import (
	"io/fs"
	"testing"
)

// embed されたファイルが期待通り公開されていることを確認する。
// PR-A 時点では htmx.min.js と css/app.css の 2 つだけ。
func TestFS_Contents(t *testing.T) {
	t.Parallel()

	root := FS()

	t.Run("htmx.min.js が読める", func(t *testing.T) {
		t.Parallel()
		data, err := fs.ReadFile(root, "htmx.min.js")
		if err != nil {
			t.Fatalf("htmx.min.js: %v", err)
		}
		if len(data) < 10_000 {
			t.Fatalf("htmx.min.js が小さすぎる (size=%d)。同梱ファイルが破損している可能性がある", len(data))
		}
	})

	t.Run("css/app.css が読める", func(t *testing.T) {
		t.Parallel()
		data, err := fs.ReadFile(root, "css/app.css")
		if err != nil {
			t.Fatalf("css/app.css: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("css/app.css が空")
		}
	})
}
