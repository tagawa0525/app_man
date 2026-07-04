package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// documents.go はライセンス証書ファイル (L-3) の web 層:
// アップロード / ダウンロード / キー閲覧 (audit_logs 記録) と、
// licenses create / update から呼ぶ FS 処理 (ディレクトリ作成・rename・
// meta.yml 再生成・fs_dir_path 衝突回避) をまとめる。

// multipartMemoryLimit は ParseMultipartForm のメモリ保持しきい値。
// 超過分は一時ファイルに落ちる (Plan の想定リスク: 20 MiB 上限なので十分)。
const multipartMemoryLimit = 32 << 20

// multipartOverheadBytes は MaxBytesReader に足す multipart 境界・
// フィールド分の余裕。ファイル本体の厳密な上限は filestore.Save が守る。
const multipartOverheadBytes = 1 << 20

// uploadDocument は POST /licenses/{id}/documents。multipart form
// (file + doc_type + note 任意) を受け、仕様 §8.3 の検証
// (サイズ上限・拡張子・マジックバイト・許可 MIME) を通った場合のみ
// fs_dir_path 配下へ保存 + license_documents 行を作る。
func (h *licenseHandlers) uploadDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	lic, err := q.GetLicenseByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license for upload", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// CSRF middleware が form 値 _csrf を読むために multipart を既に
	// パースしていることがある。その場合 body は消費済みで MaxBytesReader
	// は効かない (サイズ上限は下の Save が LimitReader で守る)。未パース
	// (X-CSRF-Token ヘッダ経由) のときだけストリーミング段階で上限を掛ける。
	if r.MultipartForm == nil {
		r.Body = http.MaxBytesReader(w, r.Body, h.fsCfg.UploadMaxBytes+multipartOverheadBytes)
	}
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		h.renderShow(w, r, id, http.StatusBadRequest,
			"アップロードを受け付けられません。ファイルサイズ上限を確認してください。")
		return
	}

	docType := strings.TrimSpace(r.PostFormValue("doc_type"))
	switch docType {
	case "certificate", "order", "other":
	default:
		h.renderShow(w, r, id, http.StatusBadRequest, "書類種別の指定が不正です。")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderShow(w, r, id, http.StatusBadRequest, "アップロードするファイルを選択してください。")
		return
	}
	defer func() { _ = file.Close() }()

	saved, err := h.store.Save(lic.FsDirPath, header.Filename, file)
	if err != nil {
		// 検証エラー (拡張子・マジックバイト・サイズ・許可 MIME)。詳細は
		// ログに残し、画面には対処できる範囲の理由だけ出す。
		h.logger.WarnContext(r.Context(), "reject document upload",
			"license_id", id, "filename", header.Filename, "err", err)
		h.renderShow(w, r, id, http.StatusBadRequest,
			"アップロードできないファイルです (PDF / PNG / JPEG のみ、サイズ上限と内容の一致を確認してください)。")
		return
	}

	var uploadedBy *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		uploadedBy = sess.AppUserID
	}
	if _, err := q.CreateLicenseDocument(r.Context(), repository.CreateLicenseDocumentParams{
		LicenseID:           id,
		DocType:             docType,
		StoredPath:          path.Join(lic.FsDirPath, saved.Name),
		OriginalFilename:    header.Filename,
		Sha256:              saved.SHA256,
		MimeType:            &saved.MIME,
		SizeBytes:           &saved.Size,
		UploadedByAppUserID: uploadedBy,
		Note:                nilIfEmpty(strings.TrimSpace(r.PostFormValue("note"))),
	}); err != nil {
		// 物理ファイルは残るが削除しない (FS が正本。DB とのズレは
		// appmgr-check-integrity が警告する)。
		h.logger.ErrorContext(r.Context(), "create license document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// meta.yml 再生成。失敗してもアップロード自体は成立させる
	// (警告のみでブロックしない思想。appmgr-generate-meta で回復可能)。
	if err := h.regenerateLicenseFS(r.Context(), q, id); err != nil {
		h.logger.ErrorContext(r.Context(), "regenerate meta.yml after upload",
			"license_id", id, "err", err)
		h.renderShow(w, r, id, http.StatusOK,
			"証書は保存しましたが meta.yml の更新に失敗しました (appmgr-generate-meta で再生成できます)。")
		return
	}

	http.Redirect(w, r, "/licenses/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// downloadDocument は GET /licenses/{id}/documents/{docID}/download。
// 認可 (viewer 以上) はルート登録側の RequireRole が担い、ここでは
// docID が URL のライセンスに属することを確認してからストリーミング配信
// する (仕様 §8.3「認可チェック後にストリーミング配信」)。
func (h *licenseHandlers) downloadDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	docID, ok := parseInt64Param(r, "docID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	doc, err := q.GetLicenseDocumentByID(r.Context(), docID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 他ライセンスの docID を URL に差し込む列挙アクセスは 404。
	if doc.LicenseID != id {
		http.NotFound(w, r)
		return
	}

	f, err := h.store.Open(doc.StoredPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// FS が正本: DB にあって FS に無いのは整合性チェック対象の
			// ズレ。ここでは 404 を返し warn ログに残す。
			h.logger.WarnContext(r.Context(), "stored document file missing",
				"license_id", id, "document_id", docID, "stored_path", doc.StoredPath)
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "open stored document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "stat stored document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if doc.MimeType != nil && *doc.MimeType != "" {
		w.Header().Set("Content-Type", *doc.MimeType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", contentDisposition(doc.OriginalFilename))
	// name は空で渡す (Content-Type は上で確定済みなので拡張子からの推測は
	// 不要)。ServeContent が Range / If-Modified-Since を処理してくれる。
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// revealKeys は POST /licenses/{id}/keys/reveal。audit_logs への INSERT が
// 成功した場合のみ平文キーを応答 (詳細画面の 1 回描画) に埋める。redirect
// すると GET 再取得でキーが出る経路になるため 200 で直接描画する。
func (h *licenseHandlers) revealKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := parseInt64Param(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := repository.New(h.db)
	row, err := q.GetLicenseByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get license for key reveal", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	keys := ""
	if row.ProductKeys != nil {
		keys = strings.TrimSpace(*row.ProductKeys)
	}
	if keys == "" {
		h.renderShow(w, r, id, http.StatusOK, "ライセンスキーは未登録です。")
		return
	}

	// 記録なしの閲覧を作らない: audit INSERT の失敗時はキーを表示せず 500。
	if err := recordAudit(r.Context(), q, r, "license_keys.view", "license", id); err != nil {
		h.logger.ErrorContext(r.Context(), "record audit for key reveal",
			"license_id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// キーを含む応答をブラウザ・中間キャッシュに残させない。
	w.Header().Set("Cache-Control", "no-store")
	h.renderShowKeys(w, r, id, http.StatusOK, "", keys)
}

// recordAudit は audit_logs へ 1 行追記する (仕様 §5.2 / §8.5)。
// app_user_id はセッションから取る。監査記録の網羅 (フェーズ 15) までは
// license_keys.view が唯一の呼び出し元。
func recordAudit(ctx context.Context, q *repository.Queries, r *http.Request, action, entityType string, entityID int64) error {
	var appUserID *int64
	if sess := middleware.SessionFrom(r.Context()); sess != nil {
		appUserID = sess.AppUserID
	}
	_, err := q.CreateAuditLog(ctx, repository.CreateAuditLogParams{
		AppUserID:  appUserID,
		Action:     action,
		EntityType: entityType,
		EntityID:   &entityID,
	})
	return err
}

// resolveLicenseFsDir は desired (仕様 §3.2 で計算した fs_dir_path) を、
// 「他ライセンスが同じ fs_dir_path を持つ (DB)」または「物理ディレクトリが
// 既存かつ空でない」場合に _2, _3... サフィックスで衝突回避して返す。
// excludeID は編集中ライセンス自身の行を DB チェックから除く (新規は 0)。
func (h *licenseHandlers) resolveLicenseFsDir(ctx context.Context, q *repository.Queries, desired string, excludeID int64) (string, error) {
	for i := 1; ; i++ {
		cand := desired
		if i > 1 {
			cand = fmt.Sprintf("%s_%d", desired, i)
		}
		n, err := q.CountLicensesByFsDirPath(ctx, repository.CountLicensesByFsDirPathParams{
			FsDirPath: cand,
			ID:        excludeID,
		})
		if err != nil {
			return "", fmt.Errorf("count licenses by fs_dir_path: %w", err)
		}
		if n > 0 {
			continue
		}
		candAbs, err := h.licenseDirAbs(cand)
		if err != nil {
			return "", fmt.Errorf("resolve fs dir candidate %s: %w", cand, err)
		}
		entries, err := os.ReadDir(candAbs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return cand, nil
			}
			return "", fmt.Errorf("inspect fs dir candidate %s: %w", cand, err)
		}
		// 空ディレクトリは再利用してよい (中身が無ければ取り違えは起きない)。
		if len(entries) == 0 {
			return cand, nil
		}
	}
}

// renameLicenseDir はスラッグ変更に物理ディレクトリを追随させる。旧が
// 無ければ何もしない (後続の regenerateLicenseFS が新パスで MkdirAll する)。
func (h *licenseHandlers) renameLicenseDir(oldDir, newDir string) error {
	oldAbs, err := h.licenseDirAbs(oldDir)
	if err != nil {
		return fmt.Errorf("resolve old license dir: %w", err)
	}
	if _, err := os.Stat(oldAbs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat old license dir %s: %w", oldAbs, err)
	}
	newAbs, err := h.licenseDirAbs(newDir)
	if err != nil {
		return fmt.Errorf("resolve new license dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", newAbs, err)
	}
	if err := prepareRenameTarget(newAbs); err != nil {
		return err
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return fmt.Errorf("rename license dir: %w", err)
	}
	return nil
}

// prepareRenameTarget は os.Rename の移動先を整える。resolveLicenseFsDir
// が「空ディレクトリは再利用可」とするため移動先に空ディレクトリが残って
// いるケースがあり、Windows の os.Rename (MoveFile) は既存ディレクトリ上
// への rename を失敗させる (Linux の rename(2) は空なら許すため OS 差で
// 挙動が割れる)。空なら除去し、空でなければ中身に触れずエラーにする
// (上書き事故防止。resolveLicenseFsDir 通過後のレースでしか起きない)。
func prepareRenameTarget(targetAbs string) error {
	entries, err := os.ReadDir(targetAbs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect rename target %s: %w", targetAbs, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("rename target %s already exists and is not empty", targetAbs)
	}
	if err := os.Remove(targetAbs); err != nil {
		return fmt.Errorf("remove empty rename target %s: %w", targetAbs, err)
	}
	return nil
}

// regenerateLicenseFS は物理ディレクトリを確保して meta.yml を現在の
// DB 内容で書き直す (仕様 §5.2 / §8.6)。ライセンス作成・更新・証書
// アップロードの 3 トリガから呼ぶ。呼び出し側でエラーをログして
// ブロックしない (FS/DB のズレは警告のみの思想)。
// 本体は appmgr-generate-meta と共有するため licensefs にある。
func (h *licenseHandlers) regenerateLicenseFS(ctx context.Context, q *repository.Queries, licenseID int64) error {
	return licensefs.Regenerate(ctx, q, h.fsCfg.BasePath, licenseID, time.Now())
}

// licenseDirAbs は fs_dir_path (/ 区切り相対) を base 配下の絶対パスにする。
// base を脱出するパスはエラー (多層防御。web の通常フローでは fs_dir_path
// を自前生成するため通らない)。
func (h *licenseHandlers) licenseDirAbs(dir string) (string, error) {
	return licensefs.DirAbs(h.fsCfg.BasePath, dir)
}

// contentDisposition は original_filename を RFC 6266 / RFC 5987 の形式で
// Content-Disposition ヘッダにする。非 ASCII を扱えない古い UA 向けの
// fallback (filename=) と、UTF-8 percent-encoding の filename*= を併記する。
func contentDisposition(name string) string {
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiFallbackFilename(name), rfc5987Encode(name))
}

// asciiFallbackFilename は quoted-string に安全な ASCII のみの代替名を作る。
// 非 ASCII・二重引用符・バックスラッシュ・制御文字は _ に置換する。
func asciiFallbackFilename(name string) string {
	out := make([]byte, 0, len(name))
	for _, r := range name {
		switch {
		case r == '"' || r == '\\' || r < 0x20 || r > 0x7e:
			out = append(out, '_')
		default:
			out = append(out, byte(r))
		}
	}
	return string(out)
}

// rfc5987Encode は RFC 5987 の value-chars (attr-char / pct-encoded) に
// 従って UTF-8 バイト列を percent-encoding する。
func rfc5987Encode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if isAttrChar(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// isAttrChar は RFC 5987 attr-char (percent-encoding 不要な文字) 判定。
func isAttrChar(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// reprefixDocumentPaths はライセンスの物理ディレクトリ移動 (oldDir →
// newDir) に合わせて、配下の証書を指す license_documents.stored_path
// (base 相対) の接頭辞を付け替える。旧接頭辞に一致しない行 (手動修正
// 済み等) はそのまま残す。
func (h *licenseHandlers) reprefixDocumentPaths(ctx context.Context, q *repository.Queries, licenseID int64, oldDir, newDir string) error {
	docs, err := q.ListLicenseDocumentsByLicense(ctx, licenseID)
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}
	prefix := oldDir + "/"
	for _, d := range docs {
		if !strings.HasPrefix(d.StoredPath, prefix) {
			continue
		}
		newPath := newDir + "/" + strings.TrimPrefix(d.StoredPath, prefix)
		affected, err := q.UpdateLicenseDocumentStoredPath(ctx, repository.UpdateLicenseDocumentStoredPathParams{
			StoredPath: newPath,
			ID:         d.ID,
		})
		if err != nil {
			return fmt.Errorf("update stored_path for document %d: %w", d.ID, err)
		}
		if affected != 1 {
			// 直前に列挙した行が消えている等の想定外。黙って進めると
			// 旧パスのままの行が残るためエラーにする
			return fmt.Errorf("update stored_path for document %d: affected %d rows, want 1", d.ID, affected)
		}
	}
	return nil
}
