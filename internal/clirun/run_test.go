package clirun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tagawa0525/app_man/internal/lockfile"
)

// writeTestConfig は最小構成の config.yml を tempdir に書き、そのパスを返す。
// logging.base_dir / locks.base_dir も tempdir 配下に隔離する。
func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	body := fmt.Sprintf(`server:
  listen: 0.0.0.0:8180
  base_url: http://localhost:8180
database:
  path: %s/app.db
  wal: true
locks:
  base_dir: %s/locks
logging:
  level: info
  base_dir: %s/logs
  format: json
`, dir, dir, dir)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// 正常系: handler が nil を返せば exit 0。
func TestRunMain_successReturnsZero(t *testing.T) {
	cfgPath := writeTestConfig(t)
	called := false
	handler := func(_ context.Context, _ Deps) error {
		called = true
		return nil
	}

	got := runMain([]string{"--config", cfgPath}, os.Stderr, "appmgr-test", lockfile.ModeShared, handler)
	if got != exitOK {
		t.Fatalf("exit code: want %d, got %d", exitOK, got)
	}
	if !called {
		t.Fatal("handler was not invoked")
	}
}

// 不正フラグ -> exit 3。
func TestRunMain_invalidFlagReturnsConfigInvalid(t *testing.T) {
	got := runMain([]string{"--bogus-flag"}, os.Stderr, "appmgr-test", lockfile.ModeShared,
		func(_ context.Context, _ Deps) error { return nil })
	if got != exitConfigInvalid {
		t.Fatalf("exit code: want %d, got %d", exitConfigInvalid, got)
	}
}

// 存在しない config パス -> exit 3。
func TestRunMain_missingConfigReturnsConfigInvalid(t *testing.T) {
	got := runMain([]string{"--config", "/nonexistent/no-such-config.yml"}, os.Stderr,
		"appmgr-test", lockfile.ModeShared,
		func(_ context.Context, _ Deps) error { return nil })
	if got != exitConfigInvalid {
		t.Fatalf("exit code: want %d, got %d", exitConfigInvalid, got)
	}
}

// handler が error を返したら exit 1。
func TestRunMain_handlerErrorReturnsHandlerError(t *testing.T) {
	cfgPath := writeTestConfig(t)
	handler := func(_ context.Context, _ Deps) error {
		return errors.New("synthetic failure")
	}
	got := runMain([]string{"--config", cfgPath}, os.Stderr, "appmgr-test", lockfile.ModeShared, handler)
	if got != exitHandlerError {
		t.Fatalf("exit code: want %d, got %d", exitHandlerError, got)
	}
}

// 既に lock が取られている状態で runMain を呼ぶと exit 2。
func TestRunMain_lockConflictReturnsLockConflict(t *testing.T) {
	cfgPath := writeTestConfig(t)

	// 同じ locks.base_dir で先に手動取得して保持
	cfgDir := filepath.Dir(cfgPath)
	first, err := lockfile.Acquire(filepath.Join(cfgDir, "locks"), "appmgr-test", lockfile.ModeShared)
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	got := runMain([]string{"--config", cfgPath}, os.Stderr, "appmgr-test", lockfile.ModeShared,
		func(_ context.Context, _ Deps) error {
			t.Fatal("handler should not be called when lock is contended")
			return nil
		})
	if got != exitLockConflict {
		t.Fatalf("exit code: want %d, got %d", exitLockConflict, got)
	}
}

// --dry-run フラグが Deps.DryRun に伝搬する。
func TestRunMain_dryRunFlagPropagates(t *testing.T) {
	cfgPath := writeTestConfig(t)
	var observed bool
	handler := func(_ context.Context, deps Deps) error {
		observed = deps.DryRun
		return nil
	}
	got := runMain([]string{"--config", cfgPath, "--dry-run"}, os.Stderr, "appmgr-test", lockfile.ModeShared, handler)
	if got != exitOK {
		t.Fatalf("exit code: want %d, got %d", exitOK, got)
	}
	if !observed {
		t.Fatal("DryRun did not propagate to handler")
	}
}
