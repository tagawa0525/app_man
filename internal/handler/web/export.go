package web

import (
	"bytes"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/export"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
	exportview "github.com/tagawa0525/app_man/internal/view/export"
)

// export.go はエクスポート画面 (仕様 §5.10 / Plan admin-export.md) の web 層:
//
//   - GET  /admin/export        説明 + Excel フォーム (include_keys) + ZIP ボタン
//   - POST /admin/export/excel  全データ Excel の配信
//   - POST /admin/export/zip    DB スナップショット + licenses/ ツリーの ZIP 配信
//
// 認可は system_admin のみ (web.go の systemAdmins 束)。ダウンロードを
// POST に限定するのは、GET だと audit 記録を迂回するリンクが作れてしまう
// ため。audit (export.excel / export.zip) は**配信開始前に**記録し、記録に
// 失敗したら配信しない (キー閲覧と同方針: 記録なしの機微データ持ち出しを
// 作らない)。生成は internal/export に分離してある。
type exportHandlers struct {
	db     *sql.DB
	logger *slog.Logger
	// fsCfg は ZIP に入れる licenses/ ツリーの BasePath を持つ。
	fsCfg config.FileStoreConfig
}

// jstZone はダウンロードファイル名のタイムスタンプ用 (Plan: JST 固定)。
// time.LoadLocation はホストの tzdata に依存するため、DST のない JST は
// FixedZone で決め打ちする。
var jstZone = time.FixedZone("JST", 9*60*60)

// exportExcelDiff は export.excel の diff_json。キーを含めた事実を監査
// できるよう、false の場合も明示的に記録する。
type exportExcelDiff struct {
	IncludeKeys bool `json:"include_keys"`
}

// exportFilename は appmgr-export-<YYYYMMDD-HHMMSS><ext> (JST)。backup の
// app-<timestamp>.db と同じく辞書順 = 時刻順になる形式。
func exportFilename(now time.Time, ext string) string {
	return "appmgr-export-" + now.In(jstZone).Format("20060102-150405") + ext
}

// index は GET /admin/export。
func (h *exportHandlers) index(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFrom(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := exportview.Index(role).Render(r.Context(), w); err != nil {
		h.logger.ErrorContext(r.Context(), "render export index", "err", err)
	}
}

// excel は POST /admin/export/excel。audit → 生成 → 配信の順で、audit の
// INSERT に失敗したら配信しない。生成はメモリ上のバッファに行い (excelize
// はもともと全量メモリ構築)、失敗時に壊れた 200 応答ではなく 500 を返せる
// ようにする。
func (h *exportHandlers) excel(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	includeKeys := r.PostFormValue("include_keys") != ""

	q := repository.New(h.db)
	if err := recordAuditDiffEntity(r.Context(), q, r, "export.excel", "export", nil,
		exportExcelDiff{IncludeKeys: includeKeys}); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for excel export", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := export.WriteExcel(r.Context(), q, &buf, includeKeys); err != nil {
		h.logger.ErrorContext(r.Context(), "write excel export", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(time.Now(), ".xlsx")+`"`)
	// キーを含みうる応答をブラウザ・中間キャッシュに残させない。
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(buf.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "send excel export", "err", err)
	}
}

// zip は POST /admin/export/zip。audit の後、応答へ直接ストリーミングする
// (DB スナップショットはバッファに載せない)。WriteZip は VACUUM INTO の
// 失敗 (appmgr-backup と並走した SQLITE_BUSY 等) を 1 バイトも書かずに
// 返すので、その場合のみ 500 に切り替える。配信途中の失敗は応答を
// 打ち切るしかないため error ログのみ残す。
func (h *exportHandlers) zip(w http.ResponseWriter, r *http.Request) {
	q := repository.New(h.db)
	if err := recordAuditDiffEntity(r.Context(), q, r, "export.zip", "export", nil, nil); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for zip export", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(time.Now(), ".zip")+`"`)
	// DB 全量 (sessions 含む) を運ぶ応答をキャッシュに残させない。
	w.Header().Set("Cache-Control", "no-store")

	cw := &countingWriter{w: w}
	if err := export.WriteZip(r.Context(), h.db, h.fsCfg.BasePath, cw); err != nil {
		h.logger.ErrorContext(r.Context(), "write zip export", "err", err)
		if cw.n == 0 {
			// まだヘッダも本文も送っていないので 500 に差し替えられる。
			// 設定済みの配信用ヘッダはエラーページに誤適用されないよう消す。
			w.Header().Del("Content-Disposition")
			w.Header().Del("Cache-Control")
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
}

// countingWriter は書き込んだバイト数を数える io.Writer。zip ハンドラが
// 「まだ何も送っていないか」(= 500 へ切替可能か) を判定するために使う。
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
