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

func seedDepartment(t *testing.T, q *repository.Queries, code, name string) repository.Department {
	t.Helper()
	d, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: code, Name: name,
	})
	if err != nil {
		t.Fatalf("seed department %q: %v", code, err)
	}
	return d
}

func TestUsersImporter_Commit_ResolvesDepartment(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	d := seedDepartment(t, q, "DEPT010", "営業部")

	csv := writeCSV(t, `employee_code,username,name,email,department_code
E001,tagawa,田川太郎,tagawa@example.com,DEPT010
E002,,山田花子,,DEPT010
E003,,自由人,,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.UsersImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}

	users, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3", len(users))
	}
	for _, u := range users {
		switch u.EmployeeCode {
		case "E001", "E002":
			if u.DepartmentID == nil || *u.DepartmentID != d.ID {
				t.Errorf("%s DepartmentID = %v, want %d", u.EmployeeCode, u.DepartmentID, d.ID)
			}
		case "E003":
			if u.DepartmentID != nil {
				t.Errorf("E003 should have nil DepartmentID")
			}
		}
	}
}

func TestUsersImporter_RejectsMissingDepartment(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `employee_code,username,name,email,department_code
E001,,田川太郎,,DEPT999
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.UsersImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want missing department error")
	}
	if !strings.Contains(out.String(), "部署 'DEPT999' が見つかりません") {
		t.Errorf("out = %q, want missing department message", out.String())
	}
}

func TestUsersImporter_RejectsEmptyEmployeeCode(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `employee_code,username,name,email,department_code
,,田川太郎,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.UsersImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want empty code error")
	}
	if !strings.Contains(out.String(), "従業員コードは必須") {
		t.Errorf("out = %q, want employee_code required", out.String())
	}
}

func TestUsersImporter_RejectsDuplicateEmployeeCode(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `employee_code,username,name,email,department_code
E001,,田川太郎,,
E001,,別人,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.UsersImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want duplicate error")
	}
	if !strings.Contains(out.String(), "CSV 内で重複") {
		t.Errorf("out = %q, want CSV duplicate message", out.String())
	}
}
