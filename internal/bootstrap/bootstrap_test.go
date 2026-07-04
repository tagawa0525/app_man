package bootstrap_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// fakeImporter は Run dispatch テスト用の制御可能 Importer。
type fakeImporter struct {
	kind           string
	header         []string
	validateErrors []bootstrap.ValidationError
	insertErr      error
	insertCalled   int
}

func (f *fakeImporter) Kind() string            { return f.kind }
func (f *fakeImporter) HeaderColumns() []string { return f.header }
func (f *fakeImporter) Validate(_ context.Context, _ *repository.Queries, _ []bootstrap.Row) []bootstrap.ValidationError {
	return f.validateErrors
}
func (f *fakeImporter) Insert(_ context.Context, q *repository.Queries, rows []bootstrap.Row) (int, error) {
	f.insertCalled = len(rows)
	// 副作用: vendors を 1 件追加する (rollback 検証用)
	if f.insertErr != nil {
		_, _ = q.CreateVendor(context.Background(), repository.CreateVendorParams{Name: "ShouldBeRolledBack"})
		return 0, f.insertErr
	}
	return len(rows), nil
}

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return p
}

func TestRun_DryRun_OutputsValidationOK(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}

	csv := writeCSV(t, "name,url,note\nA,,\nB,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, true, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "2 行検証 OK") {
		t.Errorf("out = %q, want contains '2 行検証 OK'", out.String())
	}
	if imp.insertCalled != 0 {
		t.Errorf("Insert was called in dry-run mode")
	}
}

func TestRun_Commit_CallsInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}

	csv := writeCSV(t, "name,url,note\nA,,\nB,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, false, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if imp.insertCalled != 2 {
		t.Errorf("Insert called for %d rows, want 2", imp.insertCalled)
	}
	if !strings.Contains(out.String(), "2 行投入") {
		t.Errorf("out = %q, want contains '2 行投入'", out.String())
	}
}

func TestRun_ValidationError_AbortsBeforeInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{
		kind:   "vendors",
		header: []string{"name", "url", "note"},
		validateErrors: []bootstrap.ValidationError{
			{Line: 2, Column: "name", Message: "duplicated"},
		},
	}

	csv := writeCSV(t, "name,url,note\nA,,\nA,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation error")
	}
	if imp.insertCalled != 0 {
		t.Errorf("Insert was called despite validation errors")
	}
	if !strings.Contains(out.String(), "line 2, column name: duplicated") {
		t.Errorf("out = %q, want validation error message", out.String())
	}
}

func TestRun_InsertError_RollsBack(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{
		kind:      "vendors",
		header:    []string{"name", "url", "note"},
		insertErr: errors.New("simulated db error"),
	}

	csv := writeCSV(t, "name,url,note\nA,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want insert error")
	}

	// vendors テーブルに "ShouldBeRolledBack" が残っていないことを確認
	q := repository.New(db)
	vs, err := q.ListVendors(context.Background())
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	for _, v := range vs {
		if v.Name == "ShouldBeRolledBack" {
			t.Fatalf("expected rollback, but found %q in DB", v.Name)
		}
	}
}

func TestRun_Commit_RecordsAuditLog(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}

	csv := writeCSV(t, "name,url,note\nA,,\nB,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, imp, false, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// commit 成功後に action=bootstrap_import / entity_type=kind の 1 行が
	// 記録される (受け入れ基準 15)。CLI 実行のため app_user_id は NULL。
	var (
		n        int
		diffJSON string
	)
	if err := db.QueryRow(
		`SELECT count(*) FROM audit_logs
		 WHERE action = 'bootstrap_import' AND entity_type = 'vendors'
		   AND app_user_id IS NULL AND entity_id IS NULL`,
	).Scan(&n); err != nil {
		t.Fatalf("count audit_logs: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d audit rows, want 1", n)
	}
	if err := db.QueryRow(
		`SELECT diff_json FROM audit_logs WHERE action = 'bootstrap_import'`,
	).Scan(&diffJSON); err != nil {
		t.Fatalf("read diff_json: %v", err)
	}
	if !strings.Contains(diffJSON, csv) {
		t.Errorf("diff_json = %q, want contains file path %q", diffJSON, csv)
	}
	if !strings.Contains(diffJSON, `"rows":2`) {
		t.Errorf("diff_json = %q, want contains \"rows\":2", diffJSON)
	}
}

func TestRun_DryRunOrFailure_DoesNotRecordAuditLog(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	// dry-run は記録しない
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}
	csv := writeCSV(t, "name,url,note\nA,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, imp, true, &out); err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}

	// 検証エラーでも Insert 失敗 (rollback) でも記録しない
	vimp := &fakeImporter{
		kind: "vendors", header: []string{"name", "url", "note"},
		validateErrors: []bootstrap.ValidationError{{Line: 1, Column: "name", Message: "x"}},
	}
	if err := bootstrap.Run(context.Background(), db, csv, vimp, false, &out); err == nil {
		t.Fatal("Run returned nil, want validation error")
	}
	fimp := &fakeImporter{
		kind: "vendors", header: []string{"name", "url", "note"},
		insertErr: errors.New("simulated db error"),
	}
	if err := bootstrap.Run(context.Background(), db, csv, fimp, false, &out); err == nil {
		t.Fatal("Run returned nil, want insert error")
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM audit_logs`).Scan(&n); err != nil {
		t.Fatalf("count audit_logs: %v", err)
	}
	if n != 0 {
		t.Errorf("got %d audit rows, want 0", n)
	}
}

func TestRun_HeaderMismatch_ReturnsError(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}

	csv := writeCSV(t, "wrong,header\nA,B\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, true, &out)
	if err == nil {
		t.Fatal("Run returned nil, want header mismatch error")
	}
	if !strings.Contains(err.Error(), "header mismatch") {
		t.Errorf("err = %v, want header mismatch", err)
	}
}

func TestRun_EmptyFile_ReturnsError(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	imp := &fakeImporter{kind: "vendors", header: []string{"name", "url", "note"}}

	csv := writeCSV(t, "")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, imp, true, &out)
	if err == nil {
		t.Fatal("Run returned nil, want empty file error")
	}
}
