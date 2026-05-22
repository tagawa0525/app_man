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

func TestDepartmentsImporter_Commit_ResolvesForwardParent(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT001,本社,,2020-04-01,,
DEPT010,営業部,DEPT001,2020-04-01,,
DEPT020,製造部,DEPT001,2020-04-01,,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
	ds, err := q.ListDepartments(context.Background())
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(ds) != 3 {
		t.Fatalf("got %d departments, want 3", len(ds))
	}
	for _, d := range ds {
		if d.Code == "DEPT010" || d.Code == "DEPT020" {
			if d.ParentID == nil {
				t.Errorf("dept %s: ParentID = nil, want non-nil", d.Code)
			}
		}
	}
}

func TestDepartmentsImporter_RejectsUnknownParent(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT010,営業部,DEPT999,,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want unknown parent error")
	}
	if !strings.Contains(out.String(), "親部署 'DEPT999' が未登録") {
		t.Errorf("out = %q, want unknown parent message", out.String())
	}
}

func TestDepartmentsImporter_RejectsSelfParent(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT001,本社,DEPT001,,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want self-parent error")
	}
	if !strings.Contains(out.String(), "自分自身を親") {
		t.Errorf("out = %q, want self-parent message", out.String())
	}
}

func TestDepartmentsImporter_RejectsDuplicateCode(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT001,本社,,,,
DEPT001,別の本社,,,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want duplicate code error")
	}
	if !strings.Contains(out.String(), "CSV 内で重複") {
		t.Errorf("out = %q, want CSV duplicate message", out.String())
	}
}

func TestDepartmentsImporter_RejectsInvalidDate(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT001,本社,,2020/04/01,,
`)
	var out bytes.Buffer
	err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out)
	if err == nil {
		t.Fatal("Run returned nil, want date format error")
	}
	if !strings.Contains(out.String(), "valid_from") {
		t.Errorf("out = %q, want valid_from format error", out.String())
	}
}

func TestDepartmentsImporter_AllowsParentFromDB(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	q := repository.New(db)
	if _, err := q.CreateDepartment(context.Background(), repository.CreateDepartmentParams{
		Code: "DEPT001", Name: "本社",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	csv := writeCSV(t, `code,name,parent_code,valid_from,valid_to,source_ou
DEPT010,営業部,DEPT001,,,
`)
	var out bytes.Buffer
	if err := bootstrap.Run(context.Background(), db, csv, bootstrap.DepartmentsImporter{}, false, &out); err != nil {
		t.Fatalf("Run: %v (out=%s)", err, out.String())
	}
}
