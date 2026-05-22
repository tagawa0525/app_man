// Package static は web 静的アセットを embed.FS で同梱する。
//
// 仕様書 §2 で外部 CDN 依存が禁止されているため、HTMX と CSS は
// すべてここに同梱して `/static/*` ルートで配信する。
//
// 同梱バージョン:
//   - HTMX: 1.9.12 (https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js)
//
// バージョン更新時は upstream から取得し直して static/htmx.min.js を
// 置き換え、このコメントを更新する。
package static

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// FS は `/static/*` ルート配信用のサブツリーを返す。
// 内部の `static/` プレフィックスを剥がし、Files の中身を直接ルートとする。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		// embed パスのミスは開発時に検知できるよう panic。
		// 本番 (release ビルド) で起きないことは build 時に保証される。
		panic("internal/view/static: embed sub-tree が見つかりません: " + err.Error())
	}
	return sub
}
