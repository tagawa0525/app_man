package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/tagawa0525/app_man/internal/auth"
	"github.com/tagawa0525/app_man/internal/handler/handlertest"
)

// seedLocalUser は app_users に auth_type='local' のユーザを 1 行入れ、
// 与えられたパスワードを bcrypt ハッシュとして格納する。disabled なら
// disabled_at にも値を入れる。返り値は INSERT 後の id。
func seedLocalUser(t *testing.T, db *sql.DB, username, password string, disabled bool) int64 {
	t.Helper()
	hash, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("auth.Hash: %v", err)
	}
	ctx := context.Background()
	var res sql.Result
	if disabled {
		res, err = db.ExecContext(ctx,
			`INSERT INTO app_users (username, password_hash, auth_type, disabled_at) VALUES (?, ?, 'local', ?)`,
			username, hash, time.Now())
	} else {
		res, err = db.ExecContext(ctx,
			`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, ?, 'local')`,
			username, hash)
	}
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

func seedADUser(t *testing.T, db *sql.DB, username string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES (?, NULL, 'ad')`,
		username)
	if err != nil {
		t.Fatalf("seed AD: %v", err)
	}
}

func TestLocalAuthenticator_Success(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	wantID := seedLocalUser(t, db, "admin", "passw0rd", false)

	a := auth.NewLocalAuthenticator(db)
	got, err := a.Authenticate(context.Background(), "admin", "passw0rd")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got == nil {
		t.Fatal("AuthenticatedUser is nil")
	}
	if got.ID != wantID {
		t.Errorf("ID = %d, want %d", got.ID, wantID)
	}
	if got.Username != "admin" {
		t.Errorf("Username = %q, want %q", got.Username, "admin")
	}
}

func TestLocalAuthenticator_WrongPassword(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedLocalUser(t, db, "admin", "correct-password", false)

	a := auth.NewLocalAuthenticator(db)
	_, err := a.Authenticate(context.Background(), "admin", "wrong-password")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLocalAuthenticator_UnknownUser_DoesNotLeakExistence(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	// 何もシードしない

	a := auth.NewLocalAuthenticator(db)
	_, err := a.Authenticate(context.Background(), "nobody", "anything")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials (列挙攻撃対策で wrong password と同じ)", err)
	}
}

func TestLocalAuthenticator_DisabledUser(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedLocalUser(t, db, "ex-admin", "still-correct", true)

	a := auth.NewLocalAuthenticator(db)
	_, err := a.Authenticate(context.Background(), "ex-admin", "still-correct")
	if !errors.Is(err, auth.ErrUserDisabled) {
		t.Fatalf("err = %v, want ErrUserDisabled", err)
	}
}

func TestLocalAuthenticator_ADUser_Rejected(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	seedADUser(t, db, "employee01")

	a := auth.NewLocalAuthenticator(db)
	_, err := a.Authenticate(context.Background(), "employee01", "anything")
	if !errors.Is(err, auth.ErrUnsupportedAuthType) {
		t.Fatalf("err = %v, want ErrUnsupportedAuthType", err)
	}
}

func TestLocalAuthenticator_LocalButNoPasswordHash_Rejected(t *testing.T) {
	t.Parallel()
	db := handlertest.NewTestDB(t)
	// auth_type=local で password_hash=NULL は通常起きないが、
	// ガード処理が効くことを確認する
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO app_users (username, password_hash, auth_type) VALUES ('orphan', NULL, 'local')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := auth.NewLocalAuthenticator(db)
	_, err = a.Authenticate(context.Background(), "orphan", "anything")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}
