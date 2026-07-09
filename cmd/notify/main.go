// appmgr-notify: ライセンス満了 N 日前の通知送信 (日次) と、送信失敗分の
// 再送を行う。仕様 §5.9。
//
//   - (フラグなし)     通常実行。満了通知の検出・送信と gave_up 日次サマリ
//   - --retry-failed   通常実行の代わりに status='failed' の通知を再送する
//   - --dry-run        対象件数のみログに出す (notifications へ書かない)
//
// clirun.Run は追加フラグを受け取る継ぎ目を持たないため、--retry-failed を
// 扱う本バイナリは import-bootstrap と同じく、共通機能 (config / logger /
// lock / signal) を直接呼ぶ独自 main とする (1 バイナリのための clirun 拡張は
// 早すぎる抽象化と判断。3 例目が現れたら共通化を検討する)。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/clirun"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-notify"

// exit code は clirun と揃える (要件書 §8.8)。
const (
	exitOK            = 0
	exitHandlerError  = 1
	exitLockConflict  = 2
	exitConfigInvalid = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run は本体。テストから差し替え可能になるよう stderr を io.Writer で受ける。
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.yml", "path to config.yml")
	dryRun := fs.Bool("dry-run", false, "log target counts without writing notifications")
	retryFailed := fs.Bool("retry-failed", false, "resend failed notifications instead of the daily run")
	if err := fs.Parse(args); err != nil {
		return exitConfigInvalid
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: load config: %v\n", binaryName, err)
		return exitConfigInvalid
	}

	logger, closeLog, err := applog.New(cfg.Logging, binaryName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: init logger: %v\n", binaryName, err)
		return exitConfigInvalid
	}
	defer func() {
		if cerr := closeLog(); cerr != nil {
			_, _ = fmt.Fprintf(stderr, "%s: close log: %v\n", binaryName, cerr)
		}
	}()

	lock, err := lockfile.Acquire(cfg.Locks.BaseDir, binaryName, lockfile.ModeShared)
	if err != nil {
		if errors.Is(err, lockfile.ErrAlreadyHeld) {
			logger.Warn("lock already held by another process; exiting",
				slog.String("error", err.Error()))
			return exitLockConflict
		}
		logger.Error("acquire lock", slog.Any("error", err))
		return exitHandlerError
	}
	defer func() {
		if rerr := lock.Release(); rerr != nil {
			logger.Error("release lock", slog.Any("error", rerr))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := clirun.Deps{Cfg: cfg, Logger: logger, DryRun: *dryRun}
	if err := runNotify(ctx, deps, *retryFailed, time.Now()); err != nil {
		logger.Error("notify", slog.Any("error", err))
		return exitHandlerError
	}
	return exitOK
}
