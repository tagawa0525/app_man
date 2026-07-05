package web_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/handler/middleware"
	"github.com/tagawa0525/app_man/internal/repository"
)

// audit_logs_test.go は /admin/audit-logs (仕様 §6.1 / Plan
// admin-audit-logs.md) の web 層テスト。
//
//   - GET /admin/audit-logs  監査ログ一覧 (閲覧専用、system_admin のみ)
//
// 一覧は id 降順 100 件 + 「さらに表示」(?before_id= カーソル)。フィルタは
// action 前方一致 / entity_type 完全一致 / username 完全一致。操作者は
// app_users JOIN の username 表示で、app_user_id NULL (CLI 実行) は「-」。

// seedAuditAppUser は app_users に username の行を直接 INSERT して id を
// 返す。session は作らない (audit 行の帰属先としてだけ使う)。
func seedAuditAppUser(t *testing.T, db *sql.DB, username string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, '', 'local')`,
		username)
	if err != nil {
		t.Fatalf("seedAuditAppUser(%s): %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedAuditAppUser(%s): LastInsertId: %v", username, err)
	}
	return id
}

// seedAuditLog は recordAuditDiff 相当の 1 行を repository.CreateAuditLog で
// 直接シードする。appUserID nil = CLI 起源、entityID nil = 数値 id なし。
func seedAuditLog(t *testing.T, q *repository.Queries,
	appUserID *int64, action, entityType string, entityID *int64, diffJSON *string,
) repository.AuditLog {
	t.Helper()
	row, err := q.CreateAuditLog(context.Background(), repository.CreateAuditLogParams{
		AppUserID:  appUserID,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		DiffJson:   diffJSON,
	})
	if err != nil {
		t.Fatalf("seedAuditLog(%s): %v", action, err)
	}
	return row
}

// auditRowChunk は body から marker を含む <tr>...</tr> の断片を切り出す。
// 「この行の操作者セルが - である」等、行単位の表示を検証するために使う。
func auditRowChunk(t *testing.T, rec *httptest.ResponseRecorder, marker string) string {
	t.Helper()
	body := rec.Body.String()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("body does not contain %q:\n%s", marker, body)
	}
	start := strings.LastIndex(body[:i], "<tr>")
	end := strings.Index(body[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("no <tr>...</tr> around %q", marker)
	}
	return body[start : i+end]
}

// --- 認可 -------------------------------------------------------------

// /admin/audit-logs は system_admin のみ (仕様 §6.1)。
func TestAdminAuditLogs_Authorization(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs", middleware.RoleDepartmentSecurityAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusForbidden)

	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
}

// --- 一覧表示 -----------------------------------------------------------

// 蓄積された audit 行が id 降順 (新しい順) で表示される。操作者は
// app_users JOIN の username、app_user_id NULL (CLI 起源) は「-」。
// entity_id NULL も「-」。日時は JST 変換表示 (UTC 保存 + 9h)。
// diff_json は <details> 折り畳みで生 JSON、NULL の行には出さない。
func TestAdminAuditLogs_List_DescendingWithOperator(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	opID := seedAuditAppUser(t, db, "audit_op")
	entityID := int64(7)
	diff := `{"old":"1","new":"2"}`
	first := seedAuditLog(t, q, &opID, "approval.grant", "department_product_approval", &entityID, &diff)
	seedAuditLog(t, q, nil, "bootstrap_import", "import", nil, nil)

	// JST 変換を固定日時で検証する (UTC 2026-01-02 15:04:05 → JST +9h)。
	if _, err := db.ExecContext(context.Background(),
		`UPDATE audit_logs SET occurred_at = '2026-01-02 15:04:05' WHERE id = ?`, first.ID); err != nil {
		t.Fatalf("update occurred_at: %v", err)
	}

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)

	// id 降順: 後からシードした bootstrap_import が先に出る。
	body := rec.Body.String()
	if bi, ag := strings.Index(body, "bootstrap_import"), strings.Index(body, "approval.grant"); bi < 0 || ag < 0 || bi > ag {
		t.Errorf("rows not in id-descending order: bootstrap_import at %d, approval.grant at %d", bi, ag)
	}

	// 操作者 username と JST 日時。
	granted := auditRowChunk(t, rec, "approval.grant")
	if !strings.Contains(granted, "audit_op") {
		t.Errorf("approval.grant row does not show operator username:\n%s", granted)
	}
	if !strings.Contains(granted, "2026-01-03 00:04:05") {
		t.Errorf("approval.grant row does not show JST time 2026-01-03 00:04:05:\n%s", granted)
	}
	// diff_json は <details> 折り畳みで生 JSON (HTML エスケープ済み)。
	if !strings.Contains(granted, "<summary>diff</summary>") {
		t.Errorf("approval.grant row does not fold diff_json in <details>:\n%s", granted)
	}
	if !strings.Contains(granted, "&#34;old&#34;") {
		t.Errorf("approval.grant row does not show raw diff JSON:\n%s", granted)
	}

	// CLI 起源: 操作者「-」、entity_id NULL も「-」、diff なし。
	// 日時文字列にも "-" が含まれるため、セル単位 (<td>-</td>) で数える。
	cli := auditRowChunk(t, rec, "bootstrap_import")
	if got := strings.Count(cli, "<td>-</td>"); got < 2 {
		t.Errorf("CLI-origin row should render '-' cells for operator and entity_id (got %d):\n%s", got, cli)
	}
	if strings.Contains(cli, "audit_op") {
		t.Errorf("CLI-origin row shows unrelated username:\n%s", cli)
	}
	if strings.Contains(cli, "<details>") {
		t.Errorf("row without diff_json renders <details>:\n%s", cli)
	}
}

// --- フィルタ -----------------------------------------------------------

// action は前方一致: approval. で approval.grant / approval.revoke がヒット
// し、app_setting.change は出ない。
func TestAdminAuditLogs_Filter_ActionPrefix(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	seedAuditLog(t, q, nil, "approval.grant", "department_product_approval", nil, nil)
	seedAuditLog(t, q, nil, "approval.revoke", "department_product_approval", nil, nil)
	seedAuditLog(t, q, nil, "app_setting.change", "app_setting", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs?action=approval.", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "approval.grant")
	handlertest.AssertContains(t, rec, "approval.revoke")
	assertNotContains(t, rec, "app_setting.change")
}

// entity_type は完全一致 (前方一致では絞れない)。
func TestAdminAuditLogs_Filter_EntityType(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	seedAuditLog(t, q, nil, "etype.keep", "product", nil, nil)
	seedAuditLog(t, q, nil, "etype.drop", "license", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs?entity_type=product", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "etype.keep")
	assertNotContains(t, rec, "etype.drop")

	// 完全一致: 前方一致相当の部分文字列ではヒットしない。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs?entity_type=produc", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	assertNotContains(t, rec, "etype.keep")
}

// username は完全一致。CLI 起源 (app_user_id NULL) の行はヒットしない。
func TestAdminAuditLogs_Filter_Username(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	aliceID := seedAuditAppUser(t, db, "alice_audit")
	bobID := seedAuditAppUser(t, db, "bob_audit")
	seedAuditLog(t, q, &aliceID, "uname.alice", "product", nil, nil)
	seedAuditLog(t, q, &bobID, "uname.bob", "product", nil, nil)
	seedAuditLog(t, q, nil, "uname.cli", "product", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs?username=alice_audit", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "uname.alice")
	assertNotContains(t, rec, "uname.bob")
	assertNotContains(t, rec, "uname.cli")
}

// --- カーソル -----------------------------------------------------------

// 150 行シード → 1 ページ目は新しい 100 行 + 「さらに表示」リンク
// (現在のフィルタ + before_id=最終表示行の id)。2 ページ目は残り 50 行で
// リンクなし。
func TestAdminAuditLogs_Cursor(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	ids := make([]int64, 0, 150)
	for i := 1; i <= 150; i++ {
		row := seedAuditLog(t, q, nil, fmt.Sprintf("cursor.row%03d", i), "product", nil, nil)
		ids = append(ids, row.ID)
	}

	// 1 ページ目: id 降順で row150..row051 の 100 行。
	req := handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, "/admin/audit-logs?action=cursor.", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "cursor.row150")
	handlertest.AssertContains(t, rec, "cursor.row051")
	assertNotContains(t, rec, "cursor.row050")
	// リンクは現在のフィルタを維持し、before_id は最終表示行 (row051) の id。
	handlertest.AssertContains(t, rec, "さらに表示")
	handlertest.AssertContains(t, rec, "action=cursor.")
	handlertest.AssertContains(t, rec, fmt.Sprintf("before_id=%d", ids[50]))

	// 2 ページ目: row050..row001 の 50 行、リンクなし。
	req = handlertest.AuthenticatedRequest(t, db, store,
		http.MethodGet, fmt.Sprintf("/admin/audit-logs?action=cursor.&before_id=%d", ids[50]),
		middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "cursor.row050")
	handlertest.AssertContains(t, rec, "cursor.row001")
	assertNotContains(t, rec, "cursor.row051")
	assertNotContains(t, rec, "さらに表示")
}

// before_id が非数値・負なら 400 (カーソルの偽装や桁あふれを入口で弾く)。
func TestAdminAuditLogs_InvalidBeforeID_400(t *testing.T) {
	t.Parallel()
	r, db, store, _ := newWebRouter(t)

	for _, bad := range []string{"abc", "-1"} {
		req := handlertest.AuthenticatedRequest(t, db, store,
			http.MethodGet, "/admin/audit-logs?before_id="+bad, middleware.RoleSystemAdmin, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET before_id=%q: status = %d, want 400", bad, rec.Code)
		}
	}
}

// % / _ は前方一致のリテラルとして扱う (Copilot 指摘)。action は
// app_setting.change のように正規に _ を含むため除去は不適で、実装は
// LIKE をやめ substr/length のリテラル前方一致 (ワイルドカード概念
// なし) で満たす。
func TestAdminAuditLogs_Filter_LikeMetacharsLiteral(t *testing.T) {
	t.Parallel()
	r, db, store, q := newWebRouter(t)

	seedAuditLog(t, q, nil, "app_setting.change", "app_setting", nil, nil)
	// _ をワイルドカード解釈すると "app_setting." の _ が X にもマッチする
	seedAuditLog(t, q, nil, "appXsetting.change", "app_setting", nil, nil)

	req := handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		"/admin/audit-logs?action=app_setting.", middleware.RoleSystemAdmin, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	handlertest.AssertContains(t, rec, "app_setting.change")
	if contains(rec.Body.String(), "appXsetting.change") {
		t.Errorf("underscore in filter must match literally, not as wildcard:\n%s", rec.Body.String())
	}

	// % 単独はリテラル % の前方一致 = どの行にもマッチしない
	req = handlertest.AuthenticatedRequest(t, db, store, http.MethodGet,
		"/admin/audit-logs?action=%25", middleware.RoleSystemAdmin, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	handlertest.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); contains(body, "app_setting.change") || contains(body, "appXsetting.change") {
		t.Errorf("bare %% filter must not act as match-all:\n%s", body)
	}
}
