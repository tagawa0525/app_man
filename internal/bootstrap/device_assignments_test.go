package bootstrap_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

const deviceAssignmentsHeader = "vendor_name,product_name,edition,department_code,license_slug,asset_code,note"

func TestDeviceAssignmentsImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedAssignmentTargets(t, db)

	csv := writeCSV(t, deviceAssignmentsHeader+"\n"+
		"Microsoft,Office,,D01,o365,PC-001,備考\n"+
		"Microsoft,Office,,D01,o365,PC-002,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.DeviceAssignmentsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	q := repository.New(db)
	asgs, err := q.ListActiveDeviceAssignmentsByLicense(context.Background(), refs.license.ID)
	if err != nil {
		t.Fatalf("ListActiveDeviceAssignmentsByLicense: %v", err)
	}
	if len(asgs) != 2 {
		t.Fatalf("got %d assignments, want 2", len(asgs))
	}
	byCode := map[string]repository.ListActiveDeviceAssignmentsByLicenseRow{}
	for _, a := range asgs {
		byCode[a.AssetCode] = a
	}
	a1, ok := byCode["PC-001"]
	if !ok {
		t.Fatal("missing assignment for PC-001")
	}
	if a1.Note == nil || *a1.Note != "備考" {
		t.Errorf("note = %v, want 備考", a1.Note)
	}
	if _, ok := byCode["PC-002"]; !ok {
		t.Error("missing assignment for PC-002")
	}
}

func TestDeviceAssignmentsImporter_RejectsInvalidTargets(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAssignmentTargets(t, db)

	csv := writeCSV(t, deviceAssignmentsHeader+"\n"+
		// line 1: ライセンス不在
		"Microsoft,Office,,D01,no-such,PC-001,\n"+
		// line 2: 端末不在
		"Microsoft,Office,,D01,o365,PC-999,\n"+
		// line 3: 退役端末
		"Microsoft,Office,,D01,o365,PC-900,\n"+
		// line 4: asset_code 欠落
		"Microsoft,Office,,D01,o365,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DeviceAssignmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column license_slug: ライセンス 'no-such' が見つかりません",
		"line 2, column asset_code: 端末 'PC-999' が見つかりません",
		"line 3, column asset_code: 退役済みの端末には割当できません",
		"line 4, column asset_code: 資産コードは必須です",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestDeviceAssignmentsImporter_RejectsDuplicates(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedAssignmentTargets(t, db)

	// DB 既存: PC-001 は既にアクティブ割当済み
	q := repository.New(db)
	if _, err := q.CreateDeviceAssignment(context.Background(), repository.CreateDeviceAssignmentParams{
		LicenseID: refs.license.ID, DeviceID: refs.activeDev.ID,
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	csv := writeCSV(t, deviceAssignmentsHeader+"\n"+
		// line 1: DB のアクティブ割当と重複
		"Microsoft,Office,,D01,o365,PC-001,\n"+
		// line 2 & 3: CSV 内で重複
		"Microsoft,Office,,D01,o365,PC-002,\n"+
		"Microsoft,Office,,D01,o365,PC-002,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DeviceAssignmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column asset_code: 既に割当済みです",
		"line 3, column asset_code: CSV 内で重複しています (line 2)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestDeviceAssignmentsImporter_DryRun_DoesNotInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAssignmentTargets(t, db)

	csv := writeCSV(t, deviceAssignmentsHeader+"\n"+
		"Microsoft,Office,,D01,o365,PC-001,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.DeviceAssignmentsImporter{}, true, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM device_assignments").Scan(&n); err != nil {
		t.Fatalf("count device_assignments: %v", err)
	}
	if n != 0 {
		t.Errorf("dry-run should not insert, got %d assignments", n)
	}
}
