package web_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/filestore"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/handler/web"
	"github.com/tagawa0525/app_man/internal/repository"
	"github.com/tagawa0525/app_man/internal/session"
)

// pdfContent はマジックバイト %PDF- を持つ最小のアップロード内容。
var pdfContent = []byte("%PDF-1.7\ntest-certificate-body\n")

// docsFSCfg は t.TempDir() を base にした file_store 設定 (既定値相当)。
func docsFSCfg(t *testing.T) config.FileStoreConfig {
	t.Helper()
	return config.FileStoreConfig{
		BasePath:         t.TempDir(),
		UploadMaxBytes:   20971520,
		AllowedMimeTypes: []string{"application/pdf", "image/png", "image/jpeg"},
	}
}

// newDocsRouter は filestore.Store と file_store 設定を注入した web ルータを
// 組み立てる (newWebRouter の証書テスト版。サイズ上限等を差し替えたい
// テストのために cfg を引数で受ける)。
func newDocsRouter(t *testing.T, fsCfg config.FileStoreConfig) (http.Handler, *sql.DB, session.Store, *repository.Queries) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)
	store := session.NewSQLiteStore(sqlDB)

	r := chi.NewRouter()
	r.Use(middleware.SessionMiddleware(middleware.SessionConfig{
		Store:  store,
		MaxAge: time.Hour,
		Logger: slog.New(slog.DiscardHandler),
	}))
	r.Use(middleware.AuthMiddleware(middleware.AuthConfig{
		DB:     sqlDB,
		Logger: slog.New(slog.DiscardHandler),
	}))
	r.Use(middleware.CSRFMiddleware)
	web.RegisterRoutes(r, web.Deps{
		Logger:       slog.New(slog.DiscardHandler),
		DB:           sqlDB,
		FileStore:    filestore.New(fsCfg),
		FileStoreCfg: fsCfg,
	})
	return r, sqlDB, store, repository.New(sqlDB)
}

// authenticatedMultipartPost は multipart/form-data の POST を組み立てる。
// AuthenticatedPostForm は URL エンコード専用のため、証書アップロード用に
// multipart 版を用意する。_csrf はブラウザの hidden input と同様に
// フォームフィールドとして埋める。fileName が空ならファイルパートを付けない。
func authenticatedMultipartPost(t *testing.T, db *sql.DB, store session.Store,
	target string, role middleware.Role, fields map[string]string,
	fileName string, fileContent []byte,
) *http.Request {
	t.Helper()
	cookie := handlertest.AuthenticatedAs(t, db, store, role)
	sess, err := store.GetByID(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("authenticatedMultipartPost: GetByID(%q): %v", cookie.Value, err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("_csrf", sess.CSRFToken); err != nil {
		t.Fatalf("write _csrf field: %v", err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileName != "" {
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(fileContent); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	return req
}

// auditRow は audit_logs 検証用の 1 行。
type auditRow struct {
	AppUserID  *int64
	Action     string
	EntityType string
	EntityID   *int64
}

// fetchAuditLogs は audit_logs を直接 SELECT する (一覧クエリは本 PR の
// repository スコープ外のため raw SQL)。
func fetchAuditLogs(t *testing.T, db *sql.DB) []auditRow {
	t.Helper()
	rows, err := db.Query(`SELECT app_user_id, action, entity_type, entity_id FROM audit_logs ORDER BY id`)
	if err != nil {
		t.Fatalf("select audit_logs: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.AppUserID, &r.Action, &r.EntityType, &r.EntityID); err != nil {
			t.Fatalf("scan audit_logs: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_logs: %v", err)
	}
	return out
}

// locationID は 303 の Location ヘッダ /licenses/<id> から id を取り出す。
func locationID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	loc := rec.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(loc, "/licenses/"), 10, 64)
	if err != nil {
		t.Fatalf("Location %q is not /licenses/<id>: %v", loc, err)
	}
	return id
}

// --- アップロード ---

func TestLicenseDocuments_Upload_PDF_CreatesRowFileAndMeta(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "certificate", "note": "2024 年度分"},
		"証書 2024.pdf", pdfContent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	wantLoc := fmt.Sprintf("/licenses/%d", lic.ID)
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	// DB 行: original_filename は元名のまま、sha256 / size / mime は実測値、
	// uploaded_by_app_user_id はセッションのユーザ。
	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}
	doc := docs[0]
	if doc.DocType != "certificate" {
		t.Errorf("doc_type = %q, want certificate", doc.DocType)
	}
	if doc.OriginalFilename != "証書 2024.pdf" {
		t.Errorf("original_filename = %q, want 証書 2024.pdf", doc.OriginalFilename)
	}
	sum := sha256.Sum256(pdfContent)
	wantSHA := hex.EncodeToString(sum[:])
	if doc.Sha256 != wantSHA {
		t.Errorf("sha256 = %q, want %q", doc.Sha256, wantSHA)
	}
	if doc.SizeBytes == nil || *doc.SizeBytes != int64(len(pdfContent)) {
		t.Errorf("size_bytes = %v, want %d", doc.SizeBytes, len(pdfContent))
	}
	if doc.MimeType == nil || *doc.MimeType != "application/pdf" {
		t.Errorf("mime_type = %v, want application/pdf", doc.MimeType)
	}
	if doc.UploadedByAppUserID == nil {
		t.Errorf("uploaded_by_app_user_id = nil, want session app_user id")
	}

	// 物理ファイル: fs_dir_path 配下に Slugify 済みの保存名 (スペース → _)。
	dirAbs := filepath.Join(fsCfg.BasePath, filepath.FromSlash(lic.FsDirPath))
	stored, err := os.ReadFile(filepath.Join(dirAbs, "証書_2024.pdf"))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, pdfContent) {
		t.Errorf("stored file bytes differ from upload")
	}

	// meta.yml: documents に保存名と sha256 が載る。
	meta, err := os.ReadFile(filepath.Join(dirAbs, "meta.yml"))
	if err != nil {
		t.Fatalf("read meta.yml: %v", err)
	}
	if !strings.Contains(string(meta), "証書_2024.pdf") {
		t.Errorf("meta.yml does not list stored filename:\n%s", meta)
	}
	if !strings.Contains(string(meta), wantSHA) {
		t.Errorf("meta.yml does not list sha256 %s:\n%s", wantSHA, meta)
	}
}

func TestLicenseDocuments_Upload_FakePDF_400(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	// 拡張子は .pdf だが中身はただのテキスト → マジックバイト不一致で 400。
	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "certificate"},
		"fake.pdf", []byte("this is plain text, not a pdf"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("documents = %d, want 0 (rejected upload must not create a row)", len(docs))
	}
}

func TestLicenseDocuments_Upload_Exe_400(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "certificate"},
		"tool.exe", []byte("MZ\x90\x00"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestLicenseDocuments_Upload_SizeLimitExceeded(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	fsCfg.UploadMaxBytes = 1024
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	big := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2048)...)
	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "certificate"},
		"big.pdf", big)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("documents = %d, want 0", len(docs))
	}
}

func TestLicenseDocuments_Upload_InvalidDocType_400(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "bogus"},
		"cert.pdf", pdfContent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), lic.ID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("documents = %d, want 0", len(docs))
	}
}

func TestLicenseDocuments_Upload_Viewer_403(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", lic.ID), middleware.RoleViewer,
		map[string]string{"doc_type": "certificate"},
		"cert.pdf", pdfContent)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
}

// --- ダウンロード ---

// uploadTestDocument は manager 権限でアップロードを 1 件成立させ、
// その license_documents 行を返す。
func uploadTestDocument(t *testing.T, r http.Handler, db *sql.DB, store session.Store,
	q *repository.Queries, licenseID int64, fileName string, content []byte,
) repository.LicenseDocument {
	t.Helper()
	req := authenticatedMultipartPost(t, db, store,
		fmt.Sprintf("/licenses/%d/documents", licenseID), middleware.RoleLicenseManager,
		map[string]string{"doc_type": "certificate"},
		fileName, content)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	docs, err := q.ListLicenseDocumentsByLicense(context.Background(), licenseID)
	if err != nil {
		t.Fatalf("ListLicenseDocumentsByLicense: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("no document row after upload")
	}
	return docs[len(docs)-1]
}

func TestLicenseDocuments_Download_SameBytes_Viewer200(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)
	doc := uploadTestDocument(t, r, db, store, q, lic.ID, "証書 2024.pdf", pdfContent)

	// ダウンロードは viewer 以上 (§6.1)。viewer で 200 になること自体も
	// 受け入れ基準。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		fmt.Sprintf("/licenses/%d/documents/%d/download", lic.ID, doc.ID),
		middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), pdfContent) {
		t.Errorf("downloaded bytes differ from uploaded content")
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	// original_filename が RFC 5987 (filename*=UTF-8''...) で入る。
	// 証 = %E8%A8%BC が percent-encoding されていることを確認する。
	if !strings.Contains(cd, "UTF-8''") || !strings.Contains(cd, "%E8%A8%BC") {
		t.Errorf("Content-Disposition = %q, want RFC 5987 encoded original filename", cd)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
}

func TestLicenseDocuments_Download_OtherLicenseDocID_404(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	licA := seedLicense(t, q, s, "aaa", "契約 A", nil, nil)
	licB := seedLicense(t, q, s, "bbb", "契約 B", nil, nil)
	doc := uploadTestDocument(t, r, db, store, q, licA.ID, "cert.pdf", pdfContent)

	// 他ライセンスの docID を指す URL は 404 (license_id 一致確認)。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		fmt.Sprintf("/licenses/%d/documents/%d/download", licB.ID, doc.ID),
		middleware.RoleViewer, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusNotFound)
}

// --- キー閲覧 ---

func TestLicenseKeys_Reveal_ShowsKeysAndRecordsAudit(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	req := handlertest.AuthenticatedPostForm(t, db, store,
		fmt.Sprintf("/licenses/%d/keys/reveal", lic.ID), middleware.RoleLicenseManager, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// redirect せず、その応答にだけキーを埋めて 200 描画する
	// (redirect すると GET 再取得でキーが出る経路になってしまう)。
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "SECRET-KEY-XYZ-999")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	logs := fetchAuditLogs(t, db)
	if len(logs) != 1 {
		t.Fatalf("audit_logs rows = %d, want 1", len(logs))
	}
	al := logs[0]
	if al.Action != "license_keys.view" {
		t.Errorf("action = %q, want license_keys.view", al.Action)
	}
	if al.EntityType != "license" {
		t.Errorf("entity_type = %q, want license", al.EntityType)
	}
	if al.EntityID == nil || *al.EntityID != lic.ID {
		t.Errorf("entity_id = %v, want %d", al.EntityID, lic.ID)
	}
	if al.AppUserID == nil {
		t.Errorf("app_user_id = nil, want the session app_user id")
	}
}

func TestLicenseKeys_Reveal_NoKeys_FlashWithoutAudit(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, nil)

	req := handlertest.AuthenticatedPostForm(t, db, store,
		fmt.Sprintf("/licenses/%d/keys/reveal", lic.ID), middleware.RoleLicenseManager, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "未登録")

	if logs := fetchAuditLogs(t, db); len(logs) != 0 {
		t.Errorf("audit_logs rows = %d, want 0 (no keys were shown)", len(logs))
	}
}

func TestLicenseKeys_Reveal_Viewer_403(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	req := handlertest.AuthenticatedPostForm(t, db, store,
		fmt.Sprintf("/licenses/%d/keys/reveal", lic.ID), middleware.RoleViewer, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusForbidden)
	if strings.Contains(rec.Body.String(), "SECRET-KEY-XYZ-999") {
		t.Errorf("viewer must not see raw keys, body:\n%s", rec.Body.String())
	}
	if logs := fetchAuditLogs(t, db); len(logs) != 0 {
		t.Errorf("audit_logs rows = %d, want 0", len(logs))
	}
}

func TestLicenseKeys_Reveal_NoGETRoute(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))

	// GET でキーが出る経路が存在しないこと (audit 記録の回避防止)。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		fmt.Sprintf("/licenses/%d/keys/reveal", lic.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 405 or 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SECRET-KEY-XYZ-999") {
		t.Errorf("GET must not render raw keys, body:\n%s", rec.Body.String())
	}
}

// --- 詳細画面の出し分け ---

func TestLicenses_Show_DocumentControlsByRole(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	lic := seedLicense(t, q, s, "base", "Acrobat 年間契約", nil, strPtr("SECRET-KEY-XYZ-999"))
	doc := uploadTestDocument(t, r, db, store, q, lic.ID, "証書 2024.pdf", pdfContent)

	// license_manager: 証書一覧 + アップロードフォーム + キーを表示ボタン。
	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleLicenseManager, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "証書 2024.pdf")
	handlertest.AssertContains(t, rec,
		fmt.Sprintf("/licenses/%d/documents/%d/download", lic.ID, doc.ID))
	handlertest.AssertContains(t, rec, `enctype="multipart/form-data"`)
	handlertest.AssertContains(t, rec, "キーを表示")

	// viewer: 一覧 + ダウンロードリンクは見えるが、アップロードフォームと
	// キーを表示ボタンは出ない。
	req = handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		fmt.Sprintf("/licenses/%d", lic.ID), middleware.RoleViewer, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "証書 2024.pdf")
	if strings.Contains(rec.Body.String(), `enctype="multipart/form-data"`) {
		t.Errorf("viewer must not see the upload form")
	}
	if strings.Contains(rec.Body.String(), "キーを表示") {
		t.Errorf("viewer must not see the reveal-keys button")
	}
}

// --- create / update の FS 処理 ---

func TestLicenses_Create_CreatesPhysicalDirAndMeta(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	form := validLicenseForm(s)
	form.Set("license_slug", "契約 2024")

	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	dirAbs := filepath.Join(fsCfg.BasePath, "licenses", "Adobe", "Acrobat_Pro", "契約_2024")
	fi, err := os.Stat(dirAbs)
	if err != nil || !fi.IsDir() {
		t.Fatalf("physical dir %s not created: %v", dirAbs, err)
	}
	meta, err := os.ReadFile(filepath.Join(dirAbs, "meta.yml"))
	if err != nil {
		t.Fatalf("read meta.yml: %v", err)
	}
	if !strings.Contains(string(meta), "product: Acrobat Pro") {
		t.Errorf("meta.yml missing product name:\n%s", meta)
	}
	if !strings.Contains(string(meta), "vendor: Adobe") {
		t.Errorf("meta.yml missing vendor name:\n%s", meta)
	}
}

func TestLicenses_Create_FsDirPathCollision_AppendsSuffix(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")

	// "契約 2024" と "契約/2024" は DB 上は別スラッグだが、Slugify 後の
	// fs_dir_path はどちらも .../契約_2024 になる (仕様 §3.2 の衝突ケース)。
	form1 := validLicenseForm(s)
	form1.Set("license_slug", "契約 2024")
	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form1)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first create status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	firstID := locationID(t, rec)

	form2 := validLicenseForm(s)
	form2.Set("license_slug", "契約/2024")
	req = handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form2)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second create status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	secondID := locationID(t, rec)

	first, err := q.GetLicenseByID(context.Background(), firstID)
	if err != nil {
		t.Fatalf("GetLicenseByID(first): %v", err)
	}
	second, err := q.GetLicenseByID(context.Background(), secondID)
	if err != nil {
		t.Fatalf("GetLicenseByID(second): %v", err)
	}
	if first.FsDirPath != "licenses/Adobe/Acrobat_Pro/契約_2024" {
		t.Errorf("first fs_dir_path = %q", first.FsDirPath)
	}
	if second.FsDirPath != "licenses/Adobe/Acrobat_Pro/契約_2024_2" {
		t.Errorf("second fs_dir_path = %q, want _2 suffix", second.FsDirPath)
	}
	if fi, err := os.Stat(filepath.Join(fsCfg.BasePath, filepath.FromSlash(second.FsDirPath))); err != nil || !fi.IsDir() {
		t.Errorf("suffixed physical dir not created: %v", err)
	}
}

func TestLicenses_Update_SlugChange_RenamesPhysicalDir(t *testing.T) {
	t.Parallel()
	fsCfg := docsFSCfg(t)
	r, db, store, q := newDocsRouter(t, fsCfg)

	s := seedLicenseCatalog(t, q, "Adobe", "Acrobat Pro", "DEPT001", "情報システム部")
	form := validLicenseForm(s)
	form.Set("license_slug", "改名前")
	req := handlertest.AuthenticatedPostForm(t, db, store, "/licenses", middleware.RoleLicenseManager, form)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	id := locationID(t, rec)

	oldDir := filepath.Join(fsCfg.BasePath, "licenses", "Adobe", "Acrobat_Pro", "改名前")
	if fi, err := os.Stat(oldDir); err != nil || !fi.IsDir() {
		t.Fatalf("old physical dir not created: %v", err)
	}

	upd := validLicenseForm(s)
	upd.Set("license_slug", "改名後")
	req = handlertest.AuthenticatedPostForm(t, db, store,
		fmt.Sprintf("/licenses/%d", id), middleware.RoleLicenseManager, upd)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	newDir := filepath.Join(fsCfg.BasePath, "licenses", "Adobe", "Acrobat_Pro", "改名後")
	if fi, err := os.Stat(newDir); err != nil || !fi.IsDir() {
		t.Errorf("renamed physical dir %s missing: %v", newDir, err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old physical dir %s still exists (want renamed away)", oldDir)
	}
	if _, err := os.Stat(filepath.Join(newDir, "meta.yml")); err != nil {
		t.Errorf("meta.yml missing in renamed dir: %v", err)
	}

	got, err := q.GetLicenseByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetLicenseByID: %v", err)
	}
	if got.FsDirPath != "licenses/Adobe/Acrobat_Pro/改名後" {
		t.Errorf("fs_dir_path = %q, want licenses/Adobe/Acrobat_Pro/改名後", got.FsDirPath)
	}
}
