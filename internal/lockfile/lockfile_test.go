//go:build unix

package lockfile_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/lockfile"
)

// 同一 baseDir で同名 lock を 2 回取得しに行ったとき、2 回目は
// ErrAlreadyHeld で弾かれる。本来は別プロセスからの取得が本来用途だが、
// Linux の flock(2) は同一プロセス内の別 fd 同士でも排他されるため、
// 単一プロセスのテストで再現できる。
func TestAcquire_secondCallReturnsErrAlreadyHeld(t *testing.T) {
	dir := t.TempDir()
	const name = "appmgr-test"

	first, err := lockfile.Acquire(dir, name, lockfile.ModeShared)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	if _, err := lockfile.Acquire(dir, name, lockfile.ModeShared); !errors.Is(err, lockfile.ErrAlreadyHeld) {
		t.Fatalf("second Acquire: want ErrAlreadyHeld, got %v", err)
	}
}

// 過去プロセスの異常終了で lock ファイル残骸が残っていても、
// flock(2) はプロセス終了で自動解放されるため次の Acquire が成功する。
// このテストは flock 方式の挙動を documenting し、stale PID 判定を
// 別途実装しなくて済む根拠を回帰テスト化する目的で置く。
func TestAcquire_succeedsOverLeftoverLockFile(t *testing.T) {
	dir := t.TempDir()
	const name = "appmgr-test-leftover"
	path := filepath.Join(dir, name+".lock")

	// 存在しない PID を持つメタデータを残置（前回プロセスの死骸を模す）。
	const leftover = `{"pid":999999,"started_at":"2026-01-01T00:00:00+09:00","binary":"appmgr-test-leftover"}`
	if err := os.WriteFile(path, []byte(leftover), 0o644); err != nil {
		t.Fatalf("seed leftover lock file: %v", err)
	}

	lock, err := lockfile.Acquire(dir, name, lockfile.ModeShared)
	if err != nil {
		t.Fatalf("Acquire over leftover lock file: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	// メタデータが現在の PID で上書きされていること。
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	var meta struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	if meta.PID != os.Getpid() {
		t.Fatalf("metadata PID: want %d, got %d", os.Getpid(), meta.PID)
	}
}
