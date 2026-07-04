package bootstrap_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
	"github.com/tagawa0525/app_man/internal/repository"
)

// assignmentRefs は assignments テストで使う追加 seed (ライセンスと割当先)。
type assignmentRefs struct {
	licenseRefs
	license     repository.License
	activeUser  repository.User
	secondUser  repository.User
	retiredUser repository.User // 退職済み (deactivated_at NOT NULL)
	activeDev   repository.Device
	secondDev   repository.Device
	retiredDev  repository.Device // 退役済み (retired_at NOT NULL)
}

// seedAssignmentTargets はマスタ + ライセンス 1 件 + ユーザ / 端末を投入する。
func seedAssignmentTargets(t *testing.T, db *sql.DB) assignmentRefs {
	t.Helper()
	ctx := context.Background()
	q := repository.New(db)
	refs := assignmentRefs{licenseRefs: seedLicenseMaster(t, db)}

	lic, err := q.CreateLicense(ctx, repository.CreateLicenseParams{
		ProductID: refs.office.ID, OwningDepartmentID: refs.dept.ID,
		LicenseSlug: "o365", DisplayName: "Microsoft 365",
		CountUnit: "user", ContractType: "subscription",
		FsDirPath: "licenses/Microsoft/Office/o365",
	})
	if err != nil {
		t.Fatalf("seed license: %v", err)
	}
	refs.license = lic

	mkUser := func(code, name string) repository.User {
		u, err := q.CreateUser(ctx, repository.CreateUserParams{EmployeeCode: code, Name: name})
		if err != nil {
			t.Fatalf("seed user %s: %v", code, err)
		}
		return u
	}
	refs.activeUser = mkUser("E001", "山田太郎")
	refs.secondUser = mkUser("E002", "鈴木花子")
	refs.retiredUser = mkUser("E900", "退職済子")
	if _, err := q.SoftDeleteUser(ctx, refs.retiredUser.ID); err != nil {
		t.Fatalf("soft delete user: %v", err)
	}

	mkDevice := func(code string) repository.Device {
		d, err := q.CreateDevice(ctx, repository.CreateDeviceParams{AssetCode: code})
		if err != nil {
			t.Fatalf("seed device %s: %v", code, err)
		}
		return d
	}
	refs.activeDev = mkDevice("PC-001")
	refs.secondDev = mkDevice("PC-002")
	refs.retiredDev = mkDevice("PC-900")
	if _, err := q.SoftDeleteDevice(ctx, refs.retiredDev.ID); err != nil {
		t.Fatalf("soft delete device: %v", err)
	}
	return refs
}

const userAssignmentsHeader = "vendor_name,product_name,edition,department_code,license_slug,employee_code,external_account_id,note"

func TestUserAssignmentsImporter_Commit_InsertsRows(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedAssignmentTargets(t, db)

	csv := writeCSV(t, userAssignmentsHeader+"\n"+
		"Microsoft,Office,,D01,o365,E001,taro@example.com,備考\n"+
		"Microsoft,Office,,D01,o365,E002,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.UserAssignmentsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	q := repository.New(db)
	asgs, err := q.ListActiveUserAssignmentsByLicense(context.Background(), refs.license.ID)
	if err != nil {
		t.Fatalf("ListActiveUserAssignmentsByLicense: %v", err)
	}
	if len(asgs) != 2 {
		t.Fatalf("got %d assignments, want 2", len(asgs))
	}
	byCode := map[string]repository.ListActiveUserAssignmentsByLicenseRow{}
	for _, a := range asgs {
		byCode[a.EmployeeCode] = a
	}
	a1, ok := byCode["E001"]
	if !ok {
		t.Fatal("missing assignment for E001")
	}
	if a1.ExternalAccountID == nil || *a1.ExternalAccountID != "taro@example.com" {
		t.Errorf("external_account_id = %v, want taro@example.com", a1.ExternalAccountID)
	}
	if a1.Note == nil || *a1.Note != "備考" {
		t.Errorf("note = %v, want 備考", a1.Note)
	}
	if _, ok := byCode["E002"]; !ok {
		t.Error("missing assignment for E002")
	}
}

func TestUserAssignmentsImporter_RejectsInvalidTargets(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAssignmentTargets(t, db)

	csv := writeCSV(t, userAssignmentsHeader+"\n"+
		// line 1: ライセンス不在 (slug 違い)
		"Microsoft,Office,,D01,no-such,E001,,\n"+
		// line 2: ユーザ不在
		"Microsoft,Office,,D01,o365,E999,,\n"+
		// line 3: 退職者
		"Microsoft,Office,,D01,o365,E900,,\n"+
		// line 4: employee_code 欠落
		"Microsoft,Office,,D01,o365,,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.UserAssignmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column license_slug: ライセンス 'no-such' が見つかりません",
		"line 2, column employee_code: ユーザ 'E999' が見つかりません",
		"line 3, column employee_code: 退職済みのユーザには割当できません",
		"line 4, column employee_code: 従業員コードは必須です",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestUserAssignmentsImporter_RejectsDuplicates(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	refs := seedAssignmentTargets(t, db)

	// DB 既存: E001 は既にアクティブ割当済み
	q := repository.New(db)
	if _, err := q.CreateUserAssignment(context.Background(), repository.CreateUserAssignmentParams{
		LicenseID: refs.license.ID, UserID: refs.activeUser.ID,
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	csv := writeCSV(t, userAssignmentsHeader+"\n"+
		// line 1: DB のアクティブ割当と重複
		"Microsoft,Office,,D01,o365,E001,,\n"+
		// line 2 & 3: CSV 内で重複
		"Microsoft,Office,,D01,o365,E002,,\n"+
		"Microsoft,Office,,D01,o365,E002,,\n")
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.UserAssignmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want validation errors")
	}
	for _, want := range []string{
		"line 1, column employee_code: 既に割当済みです",
		"line 3, column employee_code: CSV 内で重複しています (line 2)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want contains %q", out.String(), want)
		}
	}
}

func TestUserAssignmentsImporter_DryRun_DoesNotInsert(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedAssignmentTargets(t, db)

	csv := writeCSV(t, userAssignmentsHeader+"\n"+
		"Microsoft,Office,,D01,o365,E001,,\n")
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.UserAssignmentsImporter{}, true, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM user_assignments").Scan(&n); err != nil {
		t.Fatalf("count user_assignments: %v", err)
	}
	if n != 0 {
		t.Errorf("dry-run should not insert, got %d assignments", n)
	}
}
