package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
)

// export_test.go は /admin/export (仕様 §5.10 / Plan admin-export.md) の
// web 層テスト。
//
//   - GET  /admin/export        説明 + Excel フォーム (include_keys) + ZIP ボタン
//   - POST /admin/export/excel  audit export.excel {include_keys} を配信前に記録
//   - POST /admin/export/zip    audit export.zip を配信前に記録
//
// 認可は system_admin のみ (systemAdmins 束)。ダウンロードを POST に限定
// するのは、GET だと audit 記録を迂回するリンクが作れてしまうため。
// 「配信前に記録」の失敗注入は再現困難のため、成功時に audit 行 + 正しい
// Content-Type / Content-Disposition が揃うことと、403 では audit 行が
// 残らないことで代替検証する (指示どおり)。

// exportAuditDiffs は entity_type=export の audit 行 (entity_id IS NULL) の
// diff_json を id 順に返す。diff_json が NULL の行は nil を入れる。
func exportAuditDiffs(t *testing.T, db *sql.DB, action string) []map[string]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT diff_json FROM audit_logs
		 WHERE action = ? AND entity_type = 'export' AND entity_id IS NULL
		 ORDER BY id`, action)
	if err != nil {
		t.Fatalf("select audit_logs for %s: %v", action, err)
	}
	defer func() { _ = rows.Close() }()
	var diffs []map[string]any
	for rows.Next() {
		var diff sql.NullString
		if err := rows.Scan(&diff); err != nil {
			t.Fatalf("scan diff_json for %s: %v", action, err)
		}
		if !diff.Valid {
			diffs = append(diffs, nil)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(diff.String), &m); err != nil {
			t.Fatalf("diff_json for %s is not valid JSON: %v (raw: %s)", action, err, diff.String)
		}
		diffs = append(diffs, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_logs for %s: %v", action, err)
	}
	return diffs
}

// licensesHeaderOf は応答ボディの xlsx から licenses シートの 1 行目を返す。
func licensesHeaderOf(t *testing.T, body []byte) []string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("excelize.OpenReader: %v", err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows("licenses")
	if err != nil {
		t.Fatalf("GetRows(licenses): %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("licenses sheet has no header row")
	}
	return rows[0]
}

// --- 認可 -------------------------------------------------------------

// /admin/export 系は system_admin のみ。403 では audit 行を残さない
// (記録なしの持ち出しを作らない、の対偶: 持ち出せない操作は記録もされない)。
func TestAdminExport_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/export", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	for _, target := range []string{"/admin/export/excel", "/admin/export/zip"} {
		req = handlertest.AuthenticatedPostForm(t, db, store, target,
			middleware.RoleDepartmentSecurityAdmin, url.Values{})
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		handlertest.AssertStatus(t, rec, http.StatusForbidden)
	}

	for _, action := range []string{"export.excel", "export.zip"} {
		if diffs := exportAuditDiffs(t, db, action); len(diffs) != 0 {
			t.Errorf("audit %s count = %d, want 0 (403 では記録しない)", action, len(diffs))
		}
	}
}

// --- 画面 -------------------------------------------------------------

// GET /admin/export は説明と 2 つの POST フォーム (Excel: include_keys
// checkbox 付き / ZIP) を出す。
func TestAdminExport_Index(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/export", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, `action="/admin/export/excel"`)
	handlertest.AssertContains(t, rec, `action="/admin/export/zip"`)
	handlertest.AssertContains(t, rec, `name="include_keys"`)
}

// --- Excel ------------------------------------------------------------

// デフォルト (checkbox off) はキーなしの xlsx を配信し、audit
// export.excel {include_keys: false} を記録する。
func TestAdminExport_Excel_DefaultExcludesKeys(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/export/excel",
		middleware.RoleSystemAdmin, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("Content-Type = %q, want xlsx MIME", got)
	}
	wantDisp := regexp.MustCompile(`^attachment; filename="appmgr-export-\d{8}-\d{6}\.xlsx"$`)
	if got := rec.Header().Get("Content-Disposition"); !wantDisp.MatchString(got) {
		t.Errorf("Content-Disposition = %q, want match %s", got, wantDisp)
	}

	if header := licensesHeaderOf(t, rec.Body.Bytes()); slices.Contains(header, "product_keys") {
		t.Errorf("licenses header %v contains product_keys, want column absent by default", header)
	}

	diffs := exportAuditDiffs(t, db, "export.excel")
	if len(diffs) != 1 {
		t.Fatalf("audit export.excel count = %d, want 1", len(diffs))
	}
	if got := diffs[0]["include_keys"]; got != false {
		t.Errorf("diff include_keys = %v, want false", got)
	}
}

// include_keys checkbox on ではキー列付きの xlsx を配信し、audit の
// diff_json に include_keys: true を記録する (キー閲覧と同方針)。
func TestAdminExport_Excel_IncludeKeys(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/export/excel",
		middleware.RoleSystemAdmin, url.Values{"include_keys": {"on"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if header := licensesHeaderOf(t, rec.Body.Bytes()); !slices.Contains(header, "product_keys") {
		t.Errorf("licenses header %v does not contain product_keys, want column present", header)
	}

	diffs := exportAuditDiffs(t, db, "export.excel")
	if len(diffs) != 1 {
		t.Fatalf("audit export.excel count = %d, want 1", len(diffs))
	}
	if got := diffs[0]["include_keys"]; got != true {
		t.Errorf("diff include_keys = %v, want true", got)
	}
}

// --- ZIP --------------------------------------------------------------

// ZIP は db-snapshot.db 入りのアーカイブを配信し、audit export.zip
// (diff なし) を記録する。
func TestAdminExport_Zip(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedPostForm(t, db, store, "/admin/export/zip",
		middleware.RoleSystemAdmin, url.Values{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	wantDisp := regexp.MustCompile(`^attachment; filename="appmgr-export-\d{8}-\d{6}\.zip"$`)
	if got := rec.Header().Get("Content-Disposition"); !wantDisp.MatchString(got) {
		t.Errorf("Content-Disposition = %q, want match %s", got, wantDisp)
	}

	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	found := false
	for _, zf := range zr.File {
		if zf.Name == "db-snapshot.db" {
			found = true
		}
	}
	if !found {
		t.Errorf("zip does not contain db-snapshot.db")
	}

	diffs := exportAuditDiffs(t, db, "export.zip")
	if len(diffs) != 1 {
		t.Fatalf("audit export.zip count = %d, want 1", len(diffs))
	}
	if diffs[0] != nil {
		t.Errorf("export.zip diff_json = %v, want NULL (変更内容を持たない)", diffs[0])
	}
}
