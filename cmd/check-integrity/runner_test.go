package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/licensefs"
	"github.com/tagawa0525/app_man/internal/repository"
)

// fixedNow は meta.yml 自動生成の決定論検証用の固定時刻。
var fixedNow = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

// seedCatalog は vendor + product + 現役部署を 1 組投入する
// (licenses の FK 前提。generate-meta の runner_test と同流儀)。
func seedCatalog(t *testing.T, q *repository.Queries) (productID, deptID int64) {
	t.Helper()
	ctx := context.Background()
	v, err := q.CreateVendor(ctx, repository.CreateVendorParams{Name: "Adobe"})
	if err != nil {
		t.Fatalf("CreateVendor: %v", err)
	}
	p, err := q.CreateProduct(ctx, repository.CreateProductParams{
		VendorID:              v.ID,
		CanonicalName:         "Acrobat Pro",
		SoftwareType:          "installed",
		DefaultApprovalStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	d, err := q.CreateDepartment(ctx, repository.CreateDepartmentParams{
		Code: "DEPT001",
		Name: "情報システム部",
	})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	return p.ID, d.ID
}

// seedLicense はライセンスを 1 行投入する。
func seedLicense(t *testing.T, q *repository.Queries, productID, deptID int64, slug string) repository.License {
	t.Helper()
	lic, err := q.CreateLicense(context.Background(), repository.CreateLicenseParams{
		ProductID:          productID,
		OwningDepartmentID: deptID,
		LicenseSlug:        slug,
		DisplayName:        "Acrobat 年間契約 " + slug,
		CountUnit:          "device",
		ContractType:       "subscription",
		FsDirPath:          "licenses/adobe/acrobat-pro/" + slug,
	})
	if err != nil {
		t.Fatalf("CreateLicense (%s): %v", slug, err)
	}
	return lic
}

// newCheckEnv は in-memory DB + 空 basePath (t.TempDir) を用意する。
func newCheckEnv(t *testing.T) (*sql.DB, *repository.Queries, string) {
	t.Helper()
	sqlDB := handlertest.NewTestDB(t)
	return sqlDB, repository.New(sqlDB), t.TempDir()
}

// logLines は JSON ログを行ごとに map へパースする。
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// findLine は msg が一致する最初のログ行を返す。無ければ Fatal。
func findLine(t *testing.T, lines []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, m := range lines {
		if m["msg"] == msg {
			return m
		}
	}
	t.Fatalf("log line with msg=%q not found in: %+v", msg, lines)
	return nil
}

// TestCheckIntegrity_WarnsPerFindingAndSummarizes は所見 1 件ごとの warn
// ログ (kind / license_id / path) と kind 別サマリ info を検証する。
// 所見があっても nil を返す (exit 0。警告はブロックしない思想)。
func TestCheckIntegrity_WarnsPerFindingAndSummarizes(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newCheckEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "2024-jouki")
	if err := licensefs.Regenerate(ctx, q, basePath, lic.ID, fixedNow); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	// 実体の無い証書行 → file_missing。
	storedPath := lic.FsDirPath + "/lost.pdf"
	if _, err := q.CreateLicenseDocument(ctx, repository.CreateLicenseDocumentParams{
		LicenseID:        lic.ID,
		DocType:          "certificate",
		StoredPath:       storedPath,
		OriginalFilename: "lost.pdf",
		Sha256:           "deadbeef",
	}); err != nil {
		t.Fatalf("CreateLicenseDocument: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := checkIntegrity(ctx, sqlDB, basePath, logger, fixedNow, false); err != nil {
		t.Fatalf("checkIntegrity must return nil even with findings (exit 0), got: %v", err)
	}

	lines := logLines(t, &buf)
	warn := findLine(t, lines, "integrity finding")
	if warn["level"] != "WARN" {
		t.Errorf("finding log level: want WARN, got %v", warn["level"])
	}
	if warn["kind"] != "file_missing" {
		t.Errorf("kind: want file_missing, got %v", warn["kind"])
	}
	if got, want := warn["license_id"], float64(lic.ID); got != want {
		t.Errorf("license_id: want %v, got %v", want, got)
	}
	if warn["path"] != storedPath {
		t.Errorf("path: want %q, got %v", storedPath, warn["path"])
	}

	sum := findLine(t, lines, "check-integrity completed")
	if sum["level"] != "INFO" {
		t.Errorf("summary log level: want INFO, got %v", sum["level"])
	}
	if got := sum["total_findings"]; got != float64(1) {
		t.Errorf("total_findings: want 1, got %v", got)
	}
	if got := sum["file_missing"]; got != float64(1) {
		t.Errorf("summary file_missing: want 1, got %v", got)
	}
	if got := sum["sha256_mismatch"]; got != float64(0) {
		t.Errorf("summary sha256_mismatch: want 0, got %v", got)
	}
	if got := sum["meta_generated"]; got != float64(0) {
		t.Errorf("meta_generated: want 0, got %v", got)
	}
}

// TestCheckIntegrity_GeneratesMetaAndReportsCount は meta.yml 欠落が実行
// モードで自動生成され、サマリの meta_generated に数えられることを検証する。
func TestCheckIntegrity_GeneratesMetaAndReportsCount(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newCheckEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "2024-jouki")
	dirAbs := filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath))
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		t.Fatalf("mkdir license dir: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := checkIntegrity(ctx, sqlDB, basePath, logger, fixedNow, false); err != nil {
		t.Fatalf("checkIntegrity: %v", err)
	}

	sum := findLine(t, logLines(t, &buf), "check-integrity completed")
	if got := sum["total_findings"]; got != float64(0) {
		t.Errorf("total_findings: want 0, got %v", got)
	}
	if got := sum["meta_generated"]; got != float64(1) {
		t.Errorf("meta_generated: want 1, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(dirAbs, "meta.yml")); err != nil {
		t.Errorf("meta.yml must be generated: %v", err)
	}
}

// TestCheckIntegrity_DryRunReportsWouldGenerateMeta は dry-run が meta.yml
// を生成せず would_generate_meta として報告することを検証する。
func TestCheckIntegrity_DryRunReportsWouldGenerateMeta(t *testing.T) {
	t.Parallel()

	sqlDB, q, basePath := newCheckEnv(t)
	ctx := context.Background()
	productID, deptID := seedCatalog(t, q)
	lic := seedLicense(t, q, productID, deptID, "2024-jouki")
	dirAbs := filepath.Join(basePath, filepath.FromSlash(lic.FsDirPath))
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		t.Fatalf("mkdir license dir: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := checkIntegrity(ctx, sqlDB, basePath, logger, fixedNow, true); err != nil {
		t.Fatalf("checkIntegrity dry-run: %v", err)
	}

	sum := findLine(t, logLines(t, &buf), "check-integrity completed")
	if got := sum["dry_run"]; got != true {
		t.Errorf("dry_run: want true, got %v", got)
	}
	if got := sum["would_generate_meta"]; got != float64(1) {
		t.Errorf("would_generate_meta: want 1, got %v", got)
	}
	if got := sum["meta_generated"]; got != float64(0) {
		t.Errorf("meta_generated: want 0, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(dirAbs, "meta.yml")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create meta.yml, stat err = %v", err)
	}
}
