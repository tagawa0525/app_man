// appmgr-import-bootstrap: 既存 Excel / CSV からの一括投入。要件書 §9。
//
// 本実装は MVP コア機能のみ:
//   - --kind { vendors | products | product_aliases | departments | users | devices }
//   - --file <path>      CSV ファイルへのパス (UTF-8、ヘッダ行あり)
//   - --commit           実投入する (省略時は dry-run)
//
// 範囲外 (別 PR):
//   - audit_logs への記録
//   - --kind alias-resolve
//   - --kind licenses / assignments
//   - Shift_JIS 入力
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

	_ "modernc.org/sqlite"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/bootstrap"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
	"github.com/tagawa0525/app_man/internal/lockfile"
)

const binaryName = "appmgr-import-bootstrap"

// exit code は clirun と揃える (要件書 §8.8)。
const (
	exitOK            = 0
	exitHandlerError  = 1
	exitLockConflict  = 2
	exitConfigInvalid = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run は本体。テストから差し替え可能になるよう stdout / stderr を io.Writer
// で受ける。clirun.Run と異なり --kind / --file / --commit の追加 flag を
// 扱うため、共通機能 (config / logger / lock / signal) を直接呼ぶ独自実装。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.yml", "path to config.yml")
	kind := fs.String("kind", "", "kind to import (vendors / products / product_aliases / departments / users / devices)")
	file := fs.String("file", "", "path to CSV file")
	commit := fs.Bool("commit", false, "actually insert rows (default: dry-run)")
	if err := fs.Parse(args); err != nil {
		return exitConfigInvalid
	}
	if *kind == "" || *file == "" {
		_, _ = fmt.Fprintln(stderr, "--kind and --file are required")
		return exitConfigInvalid
	}

	importer, ok := importerByKind(*kind)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown kind: %s\n", *kind)
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
			logger.Warn("lock already held by another process; exiting", slog.String("error", err.Error()))
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

	sqlDB, closeDB, err := db.Open(cfg.Database)
	if err != nil {
		logger.Error("open db", slog.Any("error", err))
		return exitHandlerError
	}
	defer func() {
		if cerr := closeDB(); cerr != nil {
			logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("import-bootstrap starting",
		slog.String("kind", *kind),
		slog.String("file", *file),
		slog.Bool("commit", *commit),
	)

	if err := bootstrap.Run(ctx, sqlDB, *file, importer, !*commit, stdout); err != nil {
		logger.Error("bootstrap", slog.Any("error", err))
		return exitHandlerError
	}
	logger.Info("import-bootstrap done")
	return exitOK
}

// importerByKind は --kind の値から Importer を引く。
func importerByKind(kind string) (bootstrap.Importer, bool) {
	switch kind {
	case "vendors":
		return bootstrap.VendorsImporter{}, true
	case "products":
		return bootstrap.ProductsImporter{}, true
	case "product_aliases":
		return bootstrap.ProductAliasesImporter{}, true
	case "departments":
		return bootstrap.DepartmentsImporter{}, true
	case "users":
		return bootstrap.UsersImporter{}, true
	case "devices":
		return bootstrap.DevicesImporter{}, true
	default:
		return nil, false
	}
}
