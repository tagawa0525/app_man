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

func seedUser(t *testing.T, q *repository.Queries, code, name string) repository.User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), repository.CreateUserParams{
		EmployeeCode: code, Name: name,
	})
	if err != nil {
		t.Fatalf("seed user %q: %v", code, err)
	}
	return u
}

func TestDevicesImporter_Commit_ResolvesUserAndDepartment(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	d := seedDepartment(t, q, "DEPT010", "営業部")
	u := seedUser(t, q, "E001", "田川太郎")

	csv := writeCSV(t, `asset_code,hostname,primary_user_code,department_code
PC-001,tagawa-pc,E001,DEPT010
PC-002,,,DEPT010
PC-003,server-01,,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.DevicesImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	ds, _ := q.ListDevices(context.Background())
	if len(ds) != 3 {
		t.Fatalf("got %d devices, want 3", len(ds))
	}
	for _, dv := range ds {
		switch dv.AssetCode {
		case "PC-001":
			if dv.PrimaryUserID == nil || *dv.PrimaryUserID != u.ID {
				t.Errorf("PC-001 PrimaryUserID = %v, want %d", dv.PrimaryUserID, u.ID)
			}
			if dv.DepartmentID == nil || *dv.DepartmentID != d.ID {
				t.Errorf("PC-001 DepartmentID = %v, want %d", dv.DepartmentID, d.ID)
			}
		case "PC-002":
			if dv.PrimaryUserID != nil {
				t.Errorf("PC-002 PrimaryUserID should be nil")
			}
		case "PC-003":
			if dv.DepartmentID != nil {
				t.Errorf("PC-003 DepartmentID should be nil")
			}
		}
	}
}

func TestDevicesImporter_RejectsMissingUser(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `asset_code,hostname,primary_user_code,department_code
PC-001,,E999,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DevicesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want missing user error")
	}
	if !strings.Contains(out.String(), "ユーザ 'E999' が見つかりません") {
		t.Errorf("out = %q, want missing user", out.String())
	}
}

func TestDevicesImporter_RejectsDuplicateAssetCode(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `asset_code,hostname,primary_user_code,department_code
PC-001,,,
PC-001,,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DevicesImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want duplicate asset code error")
	}
	if !strings.Contains(out.String(), "CSV 内で重複") {
		t.Errorf("out = %q, want CSV duplicate", out.String())
	}
}
