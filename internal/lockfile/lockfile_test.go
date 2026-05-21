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

// ModeGlobal は batchBinaries の全 lock を排他取得するため、
// 他バッチが 1 つでも hold 中なら ErrAlreadyHeld を返す。
func TestAcquireGlobal_failsWhenAnyBatchLockHeld(t *testing.T) {
	dir := t.TempDir()

	held, err := lockfile.Acquire(dir, "appmgr-import-skysea", lockfile.ModeShared)
	if err != nil {
		t.Fatalf("Acquire appmgr-import-skysea: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	if _, err := lockfile.Acquire(dir, "appmgr-backup", lockfile.ModeGlobal); !errors.Is(err, lockfile.ErrAlreadyHeld) {
		t.Fatalf("Acquire appmgr-backup ModeGlobal: want ErrAlreadyHeld, got %v", err)
	}
}

// ModeGlobal で hold 中は、他バッチの ModeShared Acquire も弾かれる。
func TestAcquireGlobal_blocksOtherBatches(t *testing.T) {
	dir := t.TempDir()

	backup, err := lockfile.Acquire(dir, "appmgr-backup", lockfile.ModeGlobal)
	if err != nil {
		t.Fatalf("Acquire appmgr-backup ModeGlobal: %v", err)
	}
	t.Cleanup(func() { _ = backup.Release() })

	if _, err := lockfile.Acquire(dir, "appmgr-import-skysea", lockfile.ModeShared); !errors.Is(err, lockfile.ErrAlreadyHeld) {
		t.Fatalf("Acquire appmgr-import-skysea: want ErrAlreadyHeld, got %v", err)
	}
}

// ModeGlobal の部分取得後に失敗した場合、取得済みの他バッチ lock は
// 逆順 release で全解放される。Plan の「取得失敗時は逆順 release」決定の検証。
func TestAcquireGlobal_releasesPartialOnFailure(t *testing.T) {
	dir := t.TempDir()

	blocker, err := lockfile.Acquire(dir, "appmgr-import-skysea", lockfile.ModeShared)
	if err != nil {
		t.Fatalf("Acquire blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Release() })

	if _, err := lockfile.Acquire(dir, "appmgr-backup", lockfile.ModeGlobal); !errors.Is(err, lockfile.ErrAlreadyHeld) {
		t.Fatalf("Acquire appmgr-backup ModeGlobal: want ErrAlreadyHeld, got %v", err)
	}

	// 部分取得が漏れていれば appmgr-notify の単独取得も詰まる。
	other, err := lockfile.Acquire(dir, "appmgr-notify", lockfile.ModeShared)
	if err != nil {
		t.Fatalf("Acquire appmgr-notify after partial release: %v", err)
	}
	t.Cleanup(func() { _ = other.Release() })
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
