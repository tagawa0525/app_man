package web

import (
	"os"
	"path/filepath"
	"testing"
)

// prepareRenameTarget は os.Rename の移動先を整える (documents.go)。
// resolveLicenseFsDir が「空ディレクトリは再利用可」とするため、rename の
// ターゲットに空ディレクトリが残っているケースがある。Linux の rename(2)
// は空ディレクトリへの上書きを許すが、Windows の os.Rename (MoveFile) は
// 既存ディレクトリ上への rename を失敗させるため、空なら先に除去して
// おく必要がある。ここはその契約を OS 差に依存しない形で固定する
// 内部テスト。

func TestPrepareRenameTarget_AbsentTarget_NoOp(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "missing")
	if err := prepareRenameTarget(target); err != nil {
		t.Fatalf("prepareRenameTarget(absent) = %v, want nil", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target must stay absent, stat err = %v", err)
	}
}

func TestPrepareRenameTarget_EmptyDir_Removed(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := prepareRenameTarget(target); err != nil {
		t.Fatalf("prepareRenameTarget(empty dir) = %v, want nil", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("empty target dir must be removed before rename, stat err = %v", err)
	}
}

func TestPrepareRenameTarget_NonEmptyDir_ErrorAndUntouched(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(target, "meta.yml")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := prepareRenameTarget(target); err == nil {
		t.Fatal("prepareRenameTarget(non-empty dir) = nil, want error (must not clobber)")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("non-empty target must be left untouched, stat marker err = %v", err)
	}
}
