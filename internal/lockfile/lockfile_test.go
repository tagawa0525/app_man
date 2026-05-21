//go:build unix

package lockfile_test

import (
	"errors"
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
